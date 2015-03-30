package hashcatdict

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"github.com/jmmcatee/cracklord/common"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var regLastStatusIndex *regexp.Regexp
var regStatus *regexp.Regexp
var regRuleType *regexp.Regexp
var regInputMode *regexp.Regexp
var regHashTarget *regexp.Regexp
var regHashType *regexp.Regexp
var regTimeStarted *regexp.Regexp
var regTimeEstimated *regexp.Regexp
var regGPUSpeed *regexp.Regexp
var regRecovered *regexp.Regexp
var regProgress *regexp.Regexp
var regRejected *regexp.Regexp
var regGPUHWMon *regexp.Regexp

var regGetGPUCount *regexp.Regexp
var regGetNumerator *regexp.Regexp
var regGetDenominator *regexp.Regexp
var regGetPercent *regexp.Regexp

var speedMagH = map[string]float64{
	"H/s":  1,
	"kH/s": 1000,
	"MH/s": 1000000,
	"GH/s": 1000000000,
}

var speedMagK = map[string]float64{
	"H/s":  1 / 1000,
	"kH/s": 1,
	"MH/s": 1000,
	"GH/s": 1000000,
}

var speedMagM = map[string]float64{
	"H/s":  1 / 1000000,
	"kH/s": 1 / 1000,
	"MH/s": 1,
	"GH/s": 1000,
}

var speedMagG = map[string]float64{
	"H/s":  1 / 1000000000,
	"kH/s": 1 / 1000000,
	"MH/s": 1 / 1000,
	"GH/s": 1,
}

func init() {
	var err error
	regLastStatusIndex, err = regexp.Compile(`Session\.Name\.\.\.\:`)
	regStatus, err = regexp.Compile(`Status\.\.\.\.\.\.\.\.\.\:\s+(\w+)`)
	regRuleType, err = regexp.Compile(`Rules\.Type\.\.\.\.\.\:\s+(\w+)\s+\((.+)\)`)
	regInputMode, err = regexp.Compile(`Input\.Mode\.\.\.\.\.\:\s+(\w+)\s+\((.+)\)`)
	regHashTarget, err = regexp.Compile(`Hash\.Target\.\.\.\.\:\s+([0-9a-fA-F]+)`)
	regHashType, err = regexp.Compile(`Hash\.Type\.\.\.\.\.\.\:\s+(\w+)`)
	regTimeStarted, err = regexp.Compile(`Time\.Started\.\.\.\:\s+(.+)\(.+\)`)
	regTimeEstimated, err = regexp.Compile(`Time\.Estimated\.\:\s+(.+)\(.+\)`)
	regGPUSpeed, err = regexp.Compile(`Speed\.GPU\.#([\d|\*]+)\.\.\.\:\s+(\d+\.\d+)\s+(.H/s)`)
	regRecovered, err = regexp.Compile(`Recovered\.+:\s+(\d+)\/(\d+)`)
	regProgress, err = regexp.Compile(`(Progress)\.\.\.\.\.\.\.\:\s+(\d+\/\d+.+)`)
	regRejected, err = regexp.Compile(`(Rejected)\.\.\.\.\.\.\.\:\s+(\d+\/\d+.+)`)
	regGPUHWMon, err = regexp.Compile(`(HWMon\.GPU\.#\d+)\.\.\.\:\s+(.+)`)

	regGetGPUCount, err = regexp.Compile(`\#(\d)`)
	regGetNumerator, err = regexp.Compile(`(\d+\)/\d+`)
	regGetDenominator, err = regexp.Compile(`(d+\/(\d+)`)
	regGetPercent, err = regexp.Compile(`\(\d+\.\d+\%\)`)

	if err != nil {
		panic(err.Error())
	}
}

type hascatTasker struct {
	job        common.Job
	wd         string
	cmd        exec.Cmd
	start      []string
	resume     []string
	stderr     *bytes.Buffer
	stdout     *bytes.Buffer
	stderrPipe io.ReadCloser
	stdoutPipe io.ReadCloser
	stdinPipe  io.WriteCloser

	mux  sync.Mutex
	done bool
}

func newHashcatTask(j common.Job) (common.Tasker, error) {
	h := hascatTasker{}

	h.job = j

	// Build a working directory for this job
	h.wd = filepath.Join(config.WorkDir, h.job.UUID)
	err := os.Mkdir(h.wd, 700)
	if err != nil {
		// Couldn't make a directory so kill the job
		return &hascatTasker{}, errors.New("Could not create a working directory.")
	}

	// Build the arguements for hashcat
	args := []string{}

	// Get the hash type and add an argument
	htype, ok := config.HashTypes[h.job.Parameters["algorithm"]]
	if !ok {
		return &hascatTasker{}, errors.New("Could not find the algorithm provided.")
	}

	args = append(args, "-m", htype)

	// Add the rule file to use if one was given
	ruleKey, ok := h.job.Parameters["rules"]
	if ok {
		// We have a rule file, check for blank
		if ruleKey != "" {
			rulePath, ok := config.Rules[ruleKey]
			if ok {
				args = append(args, "-r", rulePath)
			}
		}
	}

	args = append(args, "--status", "--status-timer=10", "--force")

	// Add an output file
	args = append(args, "-o", filepath.Join(h.wd, "hashes-output.txt"))

	// Take the hashes given and create a file
	hashFile, err := os.Create(filepath.Join(h.wd, "hashes.txt"))
	if err != nil {
		return &hascatTasker{}, err
	}

	hashFile.WriteString(h.job.Parameters["hashes"])

	var lines int64
	linescanner := bufio.NewScanner(hashFile)
	for linescanner.Scan() {
		lines++
	}

	h.job.TotalHashes = lines

	// Append that file to the arguments
	args = append(args, filepath.Join(h.wd, "hashes.txt"))

	// Check for dictionary given
	dictKey, ok := h.job.Parameters["dictionaries"]
	if !ok {
		return &hascatTasker{}, errors.New("No dictionary provided.")
	}

	dictPath, ok := config.Dictionaries[dictKey]
	if !ok {
		return &hascatTasker{}, errors.New("Dictionary key provided was not present.")
	}

	// Add dictionary to arguments
	args = append(args, dictPath)

	log.Printf("Arguments: %v\n", args)

	// Get everything except the session identifier because the Resume command will be different
	h.start = append(h.start, "--session="+h.job.UUID)
	h.resume = append(h.resume, "--session="+h.job.UUID)
	h.resume = append(h.resume, "--restore")

	h.start = append(h.start, args...)
	h.resume = append(h.resume, args...)

	// Configure the return values
	h.job.OutputTitles = []string{"Hash", "Plaintext"}

	return &h, nil
}

func (v *hascatTasker) Status() common.Job {
	v.mux.Lock()

	index := regLastStatusIndex.FindAllStringIndex(v.stdout.String(), -1)
	if len(index) >= 1 {
		// We found a status so start processing the last status in Stdout
		status := string(v.stdout.Bytes()[index[len(index)-1][0]:])

		// Get start and estimated times
		sStartTime := regTimeStarted.FindStringSubmatch(status)
		sEstimateTime := regTimeEstimated.FindStringSubmatch(status)

		if len(sStartTime) == 1 && len(sEstimateTime) == 1 {
			log.Printf("StartTime: %s\nEstimateTime: %s\n", sStartTime[0], sEstimateTime[0])

			tStartTime, err := time.Parse("Mon Jan 2 15:04:05 2006", sStartTime[0])
			tEstimateTime, err := time.Parse("Mon Jan 2 15:04:05 2006", sEstimateTime[0])

			// See if we have ever set the start time and set it if we have not
			if v.job.StartTime.IsZero() && err == nil {
				v.job.StartTime = tStartTime
			}

			// Get the time estimate to finish and change into a progress in %
			if err == nil {
				maxTime := tEstimateTime.Sub(tStartTime).Seconds()
				runTime := tEstimateTime.Sub(time.Now()).Seconds()

				runPercent := runTime / maxTime * 100

				v.job.Progress = int(math.Floor(runPercent))

				log.Printf("RunPercent: %f\n", runPercent)
			}
		}

		// Get the speed of one or more GPUs
		speeds := regGPUSpeed.FindAllStringSubmatch(status, -1)
		log.Printf("GPU Speeds: %v\n", speeds)
		if len(speeds) > 1 {
			// We have more than one GPU so loop through and find the combined total
			for _, speedString := range speeds {
				if speedString[1] == "*" && len(speedString) == 4 {
					// We have the total so grab the pieces
					timestamp := fmt.Sprintf("%d", time.Now().Unix())

					// Check if we have a performance unit yet
					if v.job.PerformanceTitle == "" {
						// We don't so just take the one provided
						v.job.PerformanceTitle = speedString[3]

						v.job.PerformanceData[timestamp] = speedString[2]
					} else {
						// See what we need to do with the number to match our
						// original units
						var mag float64
						switch v.job.PerformanceTitle {
						case "H/s":
							mag = speedMagH[speedString[3]]
						case "kH/s":
							mag = speedMagK[speedString[3]]
						case "MH/s":
							mag = speedMagM[speedString[3]]
						case "GH/s":
							mag = speedMagG[speedString[3]]
						}

						// Convert our string into a float
						speed, err := strconv.ParseFloat(speedString[2], 64)
						if err == nil {
							// change magnitude and save as string
							v.job.PerformanceData[timestamp] = fmt.Sprintf("%f", speed*mag)
						}
					}
				}
			}
		} else if len(speeds) == 1 {
			// We have just one GPU
			speedString := speeds[0]
			if speedString[1] == "1" && len(speedString) == 4 {
				// We have the total so grab the pieces
				timestamp := fmt.Sprintf("%d", time.Now().Unix())

				// Check if we have a performance unit yet
				if v.job.PerformanceTitle == "" {
					// We don't so just take the one provided
					v.job.PerformanceTitle = speedString[3]

					v.job.PerformanceData[timestamp] = speedString[2]
				} else {
					// See what we need to do with the number to match our
					// original units
					var mag float64
					switch v.job.PerformanceTitle {
					case "H/s":
						mag = speedMagH[speedString[3]]
					case "kH/s":
						mag = speedMagK[speedString[3]]
					case "MH/s":
						mag = speedMagM[speedString[3]]
					case "GH/s":
						mag = speedMagG[speedString[3]]
					}

					// Convert our string into a float
					speed, err := strconv.ParseFloat(speedString[2], 64)
					if err == nil {
						// change magnitude and save as string
						v.job.PerformanceData[timestamp] = fmt.Sprintf("%f", speed*mag)
					}
				}
			}
		}

		// Check for number of recovered hashes
		recovered := regRecovered.FindStringSubmatch(status)
		log.Printf("Recovered Hashes: %v\n", recovered)
		if len(recovered) == 3 {
			if r, err := strconv.ParseInt(recovered[1], 10, 64); err == nil {
				v.job.CrackedHashes = r
			}

			if r, err := strconv.ParseInt(recovered[2], 10, 64); err == nil {
				v.job.TotalHashes = r
			}
		}
	}

	// Get the output results
	if file, err := os.Open(filepath.Join(v.wd, "hashes-output.txt")); err == nil {
		linescanner := bufio.NewScanner(file)
		for linescanner.Scan() {
			v.job.OutputData = append(v.job.OutputData, strings.Split(linescanner.Text(), ":"))
		}
	}

	v.stdout.Reset()

	// Run finished script
	if v.done {
		v.job.Status = common.STATUS_DONE

		v.mux.Unlock()
		return v.job
	}

	log.Printf("Job: %+v\n", v.job)

	v.mux.Unlock()
	return v.job
}

func (v *hascatTasker) Run() error {
	// Check that we have not already finished this job
	done := v.job.Status == common.STATUS_DONE || v.job.Status == common.STATUS_QUIT || v.job.Status == common.STATUS_FAILED
	if done {
		return errors.New("Job already finished.")
	}

	// Check if this job is running
	if v.job.Status == common.STATUS_RUNNING {
		// Job already running so return no errors
		return nil
	}

	// Set commands for restore or start
	if v.job.Status == common.STATUS_CREATED {
		v.cmd = *exec.Command(config.BinPath, v.start...)
	} else {
		v.cmd = *exec.Command(config.BinPath, v.resume...)
	}

	v.cmd.Dir = v.wd

	// Assign the stderr, stdout, stdin pipes
	var err error
	v.stderrPipe, err = v.cmd.StderrPipe()
	v.stdoutPipe, err = v.cmd.StdoutPipe()
	v.stdinPipe, err = v.cmd.StdinPipe()
	if err != nil {
		return err
	}

	v.stderr = bytes.NewBuffer([]byte(""))
	v.stdout = bytes.NewBuffer([]byte(""))

	go func() {
		for {
			io.Copy(v.stderr, v.stderrPipe)
		}
	}()
	go func() {
		for {
			io.Copy(v.stdout, v.stdoutPipe)
		}
	}()

	// Start the command
	err = v.cmd.Start()
	v.job.StartTime = time.Now()
	if err != nil {
		// We had an error starting to return that and quit the job
		v.job.Status = common.STATUS_FAILED
		return err
	}

	v.job.Status = common.STATUS_RUNNING

	// Build goroutine to alert that the job has finished
	go func() {
		// Listen on commmand wait and then send signal when finished
		// This will be read on the Status() function
		v.cmd.Wait()
		v.mux.Lock()
		v.done = true
		v.mux.Unlock()
	}()

	return nil
}

// Pause the hashcat run
func (v *hascatTasker) Pause() error {
	// Call status to update the job internals before pausing
	v.Status()

	// Because this is queue managed, we should just need to kill the process.
	// It will be resumed automatically
	if runtime.GOOS == "windows" {
		v.cmd.Process.Kill()
	} else {
		v.cmd.Process.Signal(syscall.SIGINT)
	}

	// Change status to pause
	v.job.Status = common.STATUS_PAUSED

	return nil
}

func (v *hascatTasker) Quit() common.Job {
	// Call status to update the job internals before quiting
	v.Status()

	if runtime.GOOS == "windows" {
		v.cmd.Process.Kill()
	} else {
		v.cmd.Process.Signal(syscall.SIGINT)
	}

	v.job.Status = common.STATUS_QUIT

	return v.job
}

func (v *hascatTasker) IOE() (io.Writer, io.Reader, io.Reader) {
	return v.stdinPipe, v.stdoutPipe, v.stderrPipe
}
