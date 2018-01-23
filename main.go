package main

import (
	"os"
	"fmt"
	"net"
	"flag"
	"math"
	"sync"
	"time"
	"bytes"
	"errors"
	"regexp"
	"strings"
	"syscall"
	"os/exec"
	"os/user"
	"os/signal"
	"io/ioutil"
	"encoding/json"
	"path/filepath"
	terminal "github.com/wayneashleyberry/terminal-dimensions"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/aws/credentials"
	//"github.com/aws/aws-sdk-go/service/ec2"
)

/*
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
~~~~~~~ global structs and vars ~~~~~~
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
*/
// start time (for log files)
var scan_start = time.Now()
var term_width, _ = terminal.Width()
var term_height, _ = terminal.Height()

var status_spin = []string{"\\","|","/","-"}

// output log file
var logging = true
var logfile_root_path string
var logfile_path string

// track number of prints to properly format 'flush' printing
var number_of_prints = 0

// arguments to scans
var hydra_default_user_wordlist = 	"/Users/coastal/Documents/tools/SecLists/Usernames/top_shortlist.txt"
var hydra_default_user_passlist = 	"/Users/coastal/Documents/tools/SecLists/Passwords/best1050.txt"
var gobuster_default_dirlist = 		"/Users/coastal/Documents/tools/SecLists/Discovery/Web_Content/raft-medium-directories.txt"
var gobuster_default_filelist = 	"/Users/coastal/Documents/tools/SecLists/Discovery/Web_Content/raft-medium-files.txt"
var smtp_default_namelist = 		"/Users/coastal/Documents/tools/SecLists/Usernames/top_shortlist.txt"

// struct to hold string value of AWS credentials
type creds struct {
	AccessKey string
	SecretKey string
}

// struct to hold information about a scan
type scan struct {
	mutex *sync.RWMutex
	scan_type string
	name string
	command string
	args []string
	results string
	status string
	elapsed float64
	logged bool
	error_message string
}

// struct to hold information about an id'd service
type service struct {
	name string
	port string
	status string
}

// color escape characters for terminal printing
var string_format = struct {
	header string
	blue string
	green string
	yellow string
	red string
	end string
	bold string
	underl string
}{
	header: "\033[95m",
	blue: 	"\033[94m",
	green: 	"\033[92m",
	yellow: "\033[93m",
	red: 	"\033[91m",
	end: 	"\033[0m",
	bold: 	"\033[1m",
	underl: "\033[4m",
}

func cleanup() {
	colorPrint("\n[!] caught Ctl-C ... cleaning up", string_format.yellow, logging, true)
	os.Exit(1)
}

func allSame(ints []int) bool {
	for i := 0; i < len(ints); i++ {
		if ints[i] != ints[0] {
			return false
		}
	}
	return true
}

func log(log_filepath string, message string) {
	if logging {
		var f *os.File
		var err error
		// path to logfile doesn't exist, create the file
		if _, err := os.Stat(log_filepath); os.IsNotExist(err) {
			f, err = os.Create(log_filepath)
			f.Close()
		}
		if err != nil {
			error_mes := fmt.Sprintf("[-] cannot create log file\n\t%v\n", err)
			colorPrint(error_mes, string_format.red, false, true)
			f.Close()
			os.Exit(1)
		}
		defer f.Close()
		f, err = os.OpenFile(log_filepath, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			error_mes := fmt.Sprintf("[-] cannot open log file\n\t%v\n", err)
			colorPrint(error_mes, string_format.red, false, true)
			f.Close()
			os.Exit(1)
		}
		to_write := fmt.Sprintf("%v\n", message)
		num, err := f.WriteString(to_write)
		if err != nil && num > 0 {
			error_mes := fmt.Sprintf("[-] cannot write to log file\n\t%v\n", err)
			colorPrint(error_mes, string_format.red, false, true)
			f.Close()
			os.Exit(1)
		}
		f.Close()
	}
}

func regularPrint(print_string string, logging bool, tracking bool) {
	if logging {
		log(logfile_path, print_string)
	}
	if tracking {
		number_of_prints += 1
	}
	fmt.Printf("%v\n", print_string)
}

func colorPrint(print_string string, color string, logging bool, tracking bool) {
	if logging {
		log(logfile_path, print_string)
	}
	if tracking {
		number_of_prints += 1
	}
	fmt.Printf("%v%v%v\n", color, print_string, string_format.end)
}

// getCreds()
//	pull aws creds from creds.json
func getCreds() creds {
	regularPrint("[*] loading aws credentials from ./creds.json", logging, true)
	var aws_creds creds
	rawjson, err := ioutil.ReadFile("./creds.json")
	if err != nil {
		print_string := fmt.Sprintf("\t[-] %v", err.Error())
		regularPrint(print_string, logging, true)
		os.Exit(1)
	}
	err = json.Unmarshal(rawjson, &aws_creds)
	if err != nil {
		print_string := fmt.Sprintf("\t[-] %v", err.Error())
		regularPrint(print_string, logging, true)
		os.Exit(1)
	}
	if len(aws_creds.AccessKey) == 0 || len(aws_creds.SecretKey) == 0 {
		regularPrint("\t[-] keys not loaded successfully", logging, true)
		os.Exit(1)
	}
	regularPrint("\t[+] creds loaded from file successfully", logging, true)
	return aws_creds
}

// returns an authenticated AWS API session object to use
// for spinning up EC2 instances
func initializeAWSSession() *session.Session{
	// load the creds
	aws_creds := getCreds()
	
	regularPrint("[*] grabbing AWS session", logging, true)

	// set environmental variables
	akid_arg := []string{"AWS_ACCESS_KEY_ID", aws_creds.AccessKey}
	secret_arg := []string{"AWS_SECRET_ACCESS_KEY", aws_creds.SecretKey}
	env_sets := [][]string{akid_arg, secret_arg}
	
	for i := 0; i < len(env_sets); i++ {
		os.Setenv(env_sets[i][0], env_sets[i][1])
	}
	// get a new aws session
	creds := credentials.NewEnvCredentials()
	sess, err := session.NewSession(&aws.Config{
		Region:      aws.String("us-west-2"),
		Credentials: creds,
	})
	// make sure we got an authenticated session
	_, crederr := sess.Config.Credentials.Get()
	if crederr != nil {
		print_string := fmt.Sprintf("\t[-] invalid credentials %v", err.Error())
		regularPrint(print_string, logging, true)
		os.Exit(0)
	}
	regularPrint("\t[+] successfully authenticated AWS session", logging, true)
	return sess
}

func scanProgress(scans []scan, target string, scan_channel chan bool) {
	start_time := time.Now()
	finished := 0
	// log the starts
	for i := 0; i < len(scans); i++ {
		to_write := fmt.Sprintf("\t[*] scan: %v (%v) [time elapsed: %.2fs]", scans[i].name, scans[i].status, scans[i].elapsed)
		if logging {
			log(logfile_path, to_write)
		}
	}
	iteration := 0
	for 1 > finished {
		iteration += 1
		var completion_statuses []int
		for i := 0; i < len(scans); i++ {
			scans[i].mutex.RLock()
			current_time := time.Now()
			time_elapsed := current_time.Sub(start_time).Seconds()
			if scans[i].status == "complete" || scans[i].status == "error" {
				completion_statuses = append(completion_statuses, 1)
				// gross..but prevents a logging:false, logged=true loop write
				if logging && (scans[i].logged == false) {
					// write our actual scan loot outputs to a log file
					scan_logfile_name := fmt.Sprintf("%v-%v-%v-.cuttlelog", target, scans[i].command, scan_start)
					scan_logfile_path := filepath.Join(logfile_root_path, scan_logfile_name)
					// log the error message if we error out
					if scans[i].status == "error" {
						if scans[i].scan_type == "cuttlefish" {
							log(scan_logfile_path, scans[i].results)
						} else {
							log(scan_logfile_path, scans[i].name)
						}
					} else {
						log(scan_logfile_path, scans[i].results)
					}
					// log the finishes to main log file
					complete_char := "+"
					if scans[i].status == "error" {
						complete_char = "!"
					}
					to_write := fmt.Sprintf("\t[%v] scan: %v (%v) [time elapsed: %.2fs]", complete_char, scans[i].name, scans[i].status, scans[i].elapsed)
					log(logfile_path, to_write)
					scans[i].logged = true
				}
			} else {
				scans[i].elapsed = time_elapsed
				completion_statuses = append(completion_statuses, 0)
			}
			scans[i].mutex.RUnlock()
		}
		if allSame(completion_statuses) && completion_statuses[0] == 1 {
			// print the final state
			outputProgress(scans, iteration)
			finished = 1

		} else {
			outputProgress(scans, iteration)
			// without the sleep output gets nuts
			time.Sleep(100000000)
		}
	}
	// update tracked prints for number of scans
	number_of_prints += len(scans)
	scan_channel <- true
}

func outputProgress(scans []scan, iteration int) {

	// this will buffer our output updating the scan results
	// without overwriting previous command line output
	output_height := (number_of_prints + 1) //% int(y)
	fmt.Printf("\033[%v;0H", output_height)
	// overwrite with blank lines 
	for i := 0; i < len(scans); i++ {
		blank_line := strings.Repeat(" ", int(term_width))
		fmt.Println(blank_line)
	}
	fmt.Printf("\033[%v;0H", output_height)
	// overwrite with updated scan results
	for i := 0; i < len(scans); i++ {
		status_character_idx := int(math.Floor(float64(iteration)/1)) % len(status_spin)
		status_character := status_spin[status_character_idx]
		if scans[i].status == "complete" {
			status_character = "+"
		} else if scans[i].status == "error" {
			status_character = "!"
		}
		to_write := fmt.Sprintf("\t[%v] scan: %v (%v) [time elapsed: %.2fs]", status_character, scans[i].name, scans[i].status, scans[i].elapsed)
		scans[i].mutex.RLock()
		if scans[i].status == "complete" {
			colorPrint(to_write, string_format.green, false, false)
		} else if scans[i].status == "running" {
			colorPrint(to_write, string_format.yellow, false, false)
		} else if scans[i].status == "error" {
			colorPrint(to_write, string_format.red, false, false)
		} else {
			colorPrint(to_write, string_format.blue, false, false)
		}
		scans[i].mutex.RUnlock()
	}
}

// identifies both open and closed running services
func identifyServices(nmap_output string) []service {
	// grab '22/tcp' from the beginning of the line
	var validService = regexp.MustCompile(`^(\d+/[a-z]+)\s+\w+\s+[A-z,a-z,0-9,-]+`)
	// grab '  ssh  ' from the validated line
	//var serviceType = regexp.MustCompile(`\s[A-z,a-z,0-9,-]+`)
	var identified_services []service
	all_lines := strings.Split(nmap_output, "\n")
	for i := 0; i < len(all_lines); i++  {
		service_string := validService.FindString(all_lines[i])
		if len(service_string) > 0 {
			service_port := strings.Split(service_string, "/")[0]

			// holy mother of hacks...not proud of this
			service_name := strings.Replace(service_string, "     ", " ", 100)
			service_name = strings.Replace(service_string, "\t", " ", 100)
			service_name = strings.Replace(service_string, "   ", " ", 100)
			service_name = strings.Replace(service_string, "  ", " ", 100)
			service_name_list := strings.Split(service_string, " ")
			// we iterate through and remove all the empty strings
			parsed_service_name_list := []string{}
			for j := 0; j < len(service_name_list); j++ {
				if len(service_name_list[j]) > 0 {
					parsed_service_name_list = append(parsed_service_name_list, service_name_list[j])
				}
			}
			service_name = parsed_service_name_list[2]
			service_status := parsed_service_name_list[1]
			new_service := service{service_name, service_port, service_status}

			identified_services = append(identified_services, new_service)
		}
	}
	return identified_services
}

func removeDuplicateServices(service_list []service) []service {
	unique_services := []service{}
	for i := 0; i < len(service_list); i++ {
		hits := 0
		for j := 0; j < len(unique_services); j++ {
			if unique_services[j].name == service_list[i].name && 
				unique_services[j].port == service_list[i].port {
					hits += 1
			}
		}
		if hits == 0 {
			unique_services = append(unique_services, service_list[i])
		}
	}
	return unique_services
}
// performs a scan for a passed command
// command is an array of strings
func performScan(target string, scan_to_perform *scan) {
	scan_to_perform.mutex.RLock()
	scan_to_perform.status = "running"
	scan_to_perform.mutex.RUnlock()
	// scope vars to be handled by both os and cuttlefish scans
	var out []byte
	var err error
	// if our scan is an external command (nmap, dirb, etc)
	if scan_to_perform.scan_type == "os" {
		out, err = exec.Command(scan_to_perform.command, scan_to_perform.args...).Output()
		
		if err != nil {
		error_string := fmt.Sprintf("%v:%v", 
			scan_to_perform.command, err)
		error_log_string := fmt.Sprintf("[!] error running (%v)\n\t%v", 
			scan_to_perform.command, err)
		if logging {
			log(logfile_path, error_log_string)
		}
		scan_to_perform.status = "error"
		scan_to_perform.name = error_string
		}
	} else {
		// if our scans are cuttlefish scans
		// if our scan is an SMTP scan
		if scan_to_perform.command == "smtp" {
			scan_channel := make(chan bool)
			go SMTPEnum(target, scan_to_perform, scan_channel)
			<-scan_channel
			out = []byte(scan_to_perform.results)
			err = errors.New(scan_to_perform.results)
		}
		if scan_to_perform.status == "error" {
			error_string := fmt.Sprintf("%v:check logs", 
				scan_to_perform.command)
			error_log_string := fmt.Sprintf("[!] error running (%v)\n\t%v", 
				scan_to_perform.command, scan_to_perform.results)
			if logging {
				log(logfile_path, error_log_string)
			}
			scan_to_perform.name = error_string
		}
	}
	
	scan_to_perform.mutex.RLock()
	scan_to_perform.results = string(out)
	if scan_to_perform.status != "error" {
		scan_to_perform.status = "complete"
	}
	scan_to_perform.mutex.RUnlock()
}

func postScanProcessing(completed_scan scan) {
	// TODO
}

func SMTPEnum(target string, scan_to_run *scan, return_channel chan bool) {
	// TODO...this is all weird. the return channel is blocking past when
	// the return bool is sent to it.

	//
	var results_buffer bytes.Buffer
	// open the enum wordlist file
	if _, e_err := os.Stat(smtp_default_namelist); os.IsNotExist(e_err) {
		error_mes := fmt.Sprintf("[!] error: SMTP namelist file does not exist: %v\n", e_err)
		results_buffer.WriteString(error_mes)
		scan_to_run.results = string(results_buffer.Bytes())
		scan_to_run.status = "error"
		return_channel <- true
	}
	results_buffer.WriteString("[*] wordlist exists\n")
	f, o_err := os.OpenFile(smtp_default_namelist, os.O_APPEND|os.O_WRONLY, 0600)
	if o_err != nil {
		error_mes := fmt.Sprintf("[!] error: cannot open SMTP namelist file: %v:\n", o_err)
		results_buffer.WriteString(error_mes)
		scan_to_run.results = string(results_buffer.Bytes())
		scan_to_run.status = "error"
		return_channel <- true
	}
	defer f.Close()
	// read the file and split the lines into names to brute force
	//		TODO
	//
	net_addr := fmt.Sprintf("%v:%v", target, scan_to_run.args[0])
	ip_addr, ip_err := net.ResolveTCPAddr("tcp4", net_addr)
	if ip_err != nil {
		error_mes := fmt.Sprintf("[!] error: could not resolve IP address (%v): %v\n", net_addr, ip_err)
		results_buffer.WriteString(error_mes)
		scan_to_run.results = string(results_buffer.Bytes())
		scan_to_run.status = "error"
		return_channel <- true
	}
	conn, c_err := net.DialTCP("tcp", nil, ip_addr)
	if c_err != nil {
		error_mes := fmt.Sprintf("[!] error: could not connect to target (%v): %v\n", net_addr, c_err)
		results_buffer.WriteString(error_mes)
		scan_to_run.results = string(results_buffer.Bytes())
		scan_to_run.status = "error"
		return_channel <- true
	}
	_, w_err := conn.Write([]byte("root"))
	if w_err != nil {
		error_mes := fmt.Sprintf("[-] could not write to target (%v): %v\n", net_addr, w_err)
		results_buffer.WriteString(error_mes)
		scan_to_run.results = string(results_buffer.Bytes())
		scan_to_run.status = "error"
		return_channel <- true
	}
	/*result, w_err := ioutil.ReadAll(conn)
	_, r_err := ioutil.ReadAll(conn)
	if r_err != nil {
		error_mes := "[-] could not read from target"
		results_buffer.WriteString(error_mes)
		scan_to_run.results = string(results_buffer.Bytes())
		scan_to_run.status = "error"
		return_channel <- true
	} //*/

	results_buffer.WriteString("this is dummy smtp scan output")
	scan_to_run.results = string(results_buffer.Bytes())
	return_channel <- true 
}

// TODO: transforms a list of services into a list of scans
// converts services identified by nmap output into `scan` structs for
// downstream processing.
func makeServiceScanList(target string, service_list []service) []scan {
	/*
	services covered by reconscan.py:
		[x] ssh
		[ ] smtp
		[ ] snmp
		[ ] domain
		[ ] ftp
		[x] http/s
		[ ] microsoft-ds
		[ ] ms-sql
	*/
	service_scan_list := []scan{}
	for i := 0; i < len(service_list); i++ {
		current_service := &service_list[i]
		// set up scans for identified services
		if current_service.name == "ssh" {
			hydra_args := []string{"-L", hydra_default_user_wordlist, "-P", hydra_default_user_passlist, "-f", "-u", target, "-s", current_service.port, "ssh"}
			new_scan := scan{&sync.RWMutex{}, "os", "ssh hydra brute", "hydra", hydra_args, "", "initialized", 0, false, ""}
			service_scan_list = append(service_scan_list, new_scan)
		} else if current_service.name == "smtp" {
			new_scan := scan{&sync.RWMutex{}, "cuttlefish", "smtp enum", "smtp", []string{current_service.port}, "", "initialized", 0, false, ""}
			service_scan_list = append(service_scan_list, new_scan)
		} else if current_service.name == "http" || current_service.name == "ssl/http" ||
			strings.Contains(current_service.name, "https") {
			gobuster_args := []string{"-u", target, "-w", gobuster_default_dirlist}
			new_scan := scan{&sync.RWMutex{}, "os", "gobuster enumeration", "gobuster", gobuster_args, "", "initialized", 0, false, ""}
			service_scan_list = append(service_scan_list, new_scan)
		}
	}
	return service_scan_list
}

func main() {
	// get her started
	// sig-term handler
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		cleanup()
		os.Exit(1)
	}()

	// get home download dir for logging
	usr, _ := user.Current()
	dir := usr.HomeDir
	default_log_dir := filepath.Join(dir, "Downloads")

	// parse out the flags passed
	target		:= flag.String("target", "d34db33f", "IP address of target machine")
	tentacles 	:= flag.Int("tentacles", 0, "number of AWS 'tentacles' (default=0)")
	output_path	:= flag.String("logfile", default_log_dir, "location of output log file")
	testing		:= flag.Bool("testing", false, "use test executables for enum output")
	flag.Parse()

	// if testing, set the os to use the CWD as an executable path
	if *testing {
		current_path := os.Getenv("PATH")
		new_path := fmt.Sprintf("$(cwd):%v", current_path)
		os.Setenv("PATH", new_path)
	}

	// set up the log file path
	cuttletarget_dir := fmt.Sprintf("%v-cuttlefish-enum", *target)
	logfile_root_path = filepath.Join(*output_path, cuttletarget_dir)
	logfile_name := fmt.Sprintf("%v-cuttlemain-%v-.cuttlelog", *target, scan_start)
	logfile_path = filepath.Join(logfile_root_path, logfile_name)

	// create the file directory if we are logging
	if logging {
		err := os.MkdirAll(logfile_root_path, os.ModePerm)
		if err != nil {
			error_mes := fmt.Sprintf("[!] could not create logging path (%v)..disabling logging", logfile_root_path)
			colorPrint(error_mes, string_format.red, false, true)
			error_str := fmt.Sprintf("error: %v", err)
			colorPrint(error_str, string_format.red, false, true)
			logging = false
		}
	}

	// clear the terminal
	print("\033[H\033[2J")
	// header strings
	cuttle_header_1 := "-------------.__   ,+-.           ,+ ~.     ,-----------"
	cuttle_header_2 := "           O  o `- o ,-`           `.o `._./            "
	cuttle_header_3 := "o   O   o   o _O  o /   cuttlefish   \\ O  o    O   o   O"
	cuttle_header_4 := "__o___O____,-`  `\\_*         v0.0     \\._____o___coastal"
	colorPrint(cuttle_header_1, string_format.blue,logging, true)
	colorPrint(cuttle_header_2, string_format.blue,logging, true)
	colorPrint(cuttle_header_3, string_format.blue,logging, true)
	colorPrint(cuttle_header_4, string_format.blue,logging, true)
	// var aws_session *session.Session
	if *target == "d34db33f" {
		colorPrint("[!] specify a target with '-target=TARGET_IP'", string_format.red, logging, true)
		os.Exit(0)
	}
	if *tentacles > 0 {
		// aws_session := initializeAWSSession()
		initializeAWSSession()	
	}
	// runmode summary
	opt_1 := fmt.Sprintf("[*] run options")
	logfile_path_string := fmt.Sprintf("\t[*] logging to %v", logfile_root_path)
	opt_2 := fmt.Sprintf("\t[*] target:\t\t%v", *target)
	opt_3 := fmt.Sprintf("\t[*] aws tentacles:\t%v", *tentacles)
	regularPrint(opt_1, logging, true)
	if logging {
		regularPrint(logfile_path_string, logging, true)
	}
	regularPrint(opt_2, logging, true)
	regularPrint(opt_3, logging, true)
	
	// initialize the scans
	var scans []scan
	nmap_tcp_scan := scan{&sync.RWMutex{}, "os", "initial nmap TCP recon", "nmap", []string{}, "", "initialized", 0, false, ""}
	nmap_udp_scan := scan{&sync.RWMutex{}, "os", "initial nmap UDP recon", "nmap", []string{}, "", "initialized", 0, false, ""}
	if os.Getuid() == 0 {
		getuid_string := fmt.Sprintf("[+] root privs enabled (GUID: %v), script scanning with nmap", os.Getuid())
		colorPrint(getuid_string, string_format.green, logging, true)
		nmap_tcp_scan.args = []string{"-vv", "-Pn", "-A", "-sC", "-sS", "-p-", *target}
		nmap_udp_scan.args = []string{"-vv", "-Pn", "-A", "-sC", "-sU", "--top-ports", "200"}
		scans = append(scans, nmap_tcp_scan)
		scans = append(scans, nmap_udp_scan)
	} else {
		getuid_string := fmt.Sprintf("[!] not executed as root (GUID: %v), script scanning not performed", os.Getuid())
		colorPrint(getuid_string, string_format.yellow, logging, true)
		//nmap_tcp_scan.args = []string{"-vv", "-Pn", "-A", "-p-", *target}
		nmap_tcp_scan.args = []string{*target}
		// don't bother with UDP since we can't w/o root
		scans = append(scans, nmap_tcp_scan)
	}

	// setup the scan channel
	recon_scan_channel := make(chan bool)
	
	// pass by reference so we update the shared struct value
	colorPrint("[*] starting intial nmap recon scan", string_format.blue,logging, true)
	for i := 0; i < len(scans); i++ {
		go performScan(*target, &scans[i])
	}
	go scanProgress(scans, *target, recon_scan_channel)
	// block on recon scan channel 
	<-recon_scan_channel
	
	// now let's find services from the recon scan results
	identified_services := identifyServices(scans[0].results)
	if os.Getuid() == 0 {
		identified_udp_services := identifyServices(scans[1].results)
		identified_services := append(identified_services, identified_udp_services...)
		identified_services = removeDuplicateServices(identified_services)
	}
	if len(identified_services) == 0 {
		colorPrint("[-] no services identified", string_format.red, logging, true)
		colorPrint("\t[!] try different scan options", string_format.yellow, logging, true)
		os.Exit(0)
	}
	colorPrint("[+] identified running services", string_format.blue, logging, true)
	for i := 0; i < len(identified_services); i++ {
		if identified_services[i].status == "open" {
			service_string := fmt.Sprintf("\t[+] %v (%v)", 
			identified_services[i].name,
			identified_services[i].port)
			colorPrint(service_string, string_format.green, logging, true)
		} else {
			service_string := fmt.Sprintf("\t[-] %v (%v)", 
			identified_services[i].name,
			identified_services[i].port)
			colorPrint(service_string, string_format.red, logging, true)
		}
	}
	colorPrint("[*] starting follow up scans on identified services", string_format.blue, logging, true)
	// start new scans based on the service info
	scans = makeServiceScanList(*target, identified_services)
	service_scan_channel := make(chan bool)
	for i := 0; i < len(scans); i++ {
		go performScan(*target, &scans[i])
	}
	go scanProgress(scans, *target, service_scan_channel)
	<-service_scan_channel

	// process the post-scan results and perform any subsequent analysis
	// or further enum/discovery
	for i := 0; i < len(scans); i++ {
		postScanProcessing(scans[i])
	}

	complete_string := fmt.Sprintf("[+] cuttlefish enumeration of %v complete!\n", *target)
	regularPrint(complete_string, logging, true)
}





