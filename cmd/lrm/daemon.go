package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lrm-project/lrm/mobile/internal/platform"
)

// Android does not let a background process run indefinitely. The system
// kills anything it considers idle, most aggressively once the screen is
// off, and there is no way for an ordinary process to opt out. Three
// things actually help, and LRM Mobile can do two of them for you:
//
//  1. a wake lock, so the CPU is not suspended. Termux exposes one through
//     termux-wake-lock. We can acquire it and release it on exit.
//  2. restarting after a kill. LRM's sync is resumable by design, so a
//     daemon that comes back loses nothing but time. A supervisor loop is
//     the difference between "sync stopped hours ago" and "sync paused for
//     forty seconds".
//  3. exempting Termux from battery optimisation. That is a system settings
//     screen; only you can do it, so we print the instructions.
//
// These are opt-in flags rather than default behaviour: a wake lock costs
// battery, and silently holding one would be rude.

const daemonExtraUsage = `
LRM Mobile additions to 'lrm daemon':

  --supervise      restart the daemon if Android kills it (recommended on a phone)
  --wake-lock      hold a Termux wake lock while running (needs termux-api)
  --max-restarts N give up after N restarts (default 0 = never give up)

Everything else is passed through to LRM's own daemon unchanged.
`

// A daemon that dies within fastFailWindow with an ordinary exit status
// did not get killed by the system; it failed to start. After
// maxFastFailures of those in a row the supervisor stops.
const (
	fastFailWindow  = 5 * time.Second
	maxFastFailures = 3
)

type daemonOpts struct {
	supervise   bool
	wakeLock    bool
	maxRestarts int
	rest        []string // arguments to hand to upstream
}

// parseDaemonArgs splits our flags out of the daemon command line.
func parseDaemonArgs(args []string) (daemonOpts, error) {
	var o daemonOpts
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--supervise":
			o.supervise = true
		case "--wake-lock":
			o.wakeLock = true
		case "--max-restarts":
			if i+1 >= len(args) {
				return o, fmt.Errorf("--max-restarts needs a number")
			}
			i++
			n := 0
			if _, err := fmt.Sscanf(args[i], "%d", &n); err != nil || n < 0 {
				return o, fmt.Errorf("--max-restarts wants a non-negative number")
			}
			o.maxRestarts = n
		default:
			o.rest = append(o.rest, args[i])
		}
	}
	return o, nil
}

// acquireWakeLock takes a Termux wake lock and returns a release function.
func acquireWakeLock() (release func(), note string) {
	if !platform.HasTermuxAPI() {
		return func() {}, "wake lock unavailable: run `pkg install termux-api` and install the Termux:API app from F-Droid"
	}
	if err := platform.Command("termux-wake-lock").Run(); err != nil {
		return func() {}, fmt.Sprintf("could not acquire a wake lock: %v", err)
	}
	return func() {
		_ = platform.Command("termux-wake-unlock").Run()
	}, ""
}

// batteryAdvice is printed once when a daemon starts on a phone, because
// the single most effective fix is a settings change we cannot make.
const batteryAdvice = `mobile: Android will suspend or kill background processes.
mobile:   1. exempt Termux from battery optimisation:
mobile:      Settings > Apps > Termux > Battery > Unrestricted
mobile:   2. keep the session alive across disconnects:  pkg install tmux && tmux
mobile:   3. hold the CPU awake:  lrm daemon --wake-lock --supervise`

// superviseDaemon re-runs this binary's daemon command until it is asked to
// stop. Restarts are backed off so a daemon that fails instantly — a bad
// port, a missing repo — does not spin.
//
// It re-executes rather than restarting in-process because a killed daemon
// may have left the runtime in any state, and because on Android the kill
// usually takes the whole process anyway.
func superviseDaemon(o daemonOpts, rawArgs []string, info platform.Info) int {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	childArgs := append([]string{"daemon"}, o.rest...)
	backoff := time.Second
	const maxBackoff = 60 * time.Second
	restarts := 0
	fastFailures := 0

	fmt.Println("mobile: supervising `lrm daemon` — it will be restarted if Android kills it")
	fmt.Println("mobile: Ctrl-C to stop for good")

	for {
		cmd := platform.SelfCommand(rawArgs, childArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		cmd.Env = append(os.Environ(), "LRM_SUPERVISED=1")

		started := time.Now()
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "lrm: cannot start the daemon: %v\n", err)
			return 1
		}

		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		select {
		case <-stop:
			fmt.Println("\nmobile: stopping the daemon")
			if cmd.Process != nil {
				_ = cmd.Process.Signal(syscall.SIGTERM)
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					_ = cmd.Process.Kill()
				}
			}
			return 0

		case err := <-done:
			ran := time.Since(started)
			killed := isProbablyOOMKill(err)

			// A daemon that ran for a while and then died was almost
			// certainly killed by the system; reset the backoff so it
			// comes straight back.
			if ran > 30*time.Second {
				backoff = time.Second
				fastFailures = 0
			}

			// A daemon that exits immediately with an ordinary status
			// has not been killed by Android -- it has failed to start.
			// Restarting cannot help, and a supervisor that loops on it
			// forever buries the real error in its own noise. Stop and
			// show it.
			if err != nil && !killed && ran < fastFailWindow {
				fastFailures++
				if fastFailures >= maxFastFailures {
					fmt.Fprintf(os.Stderr,
						"\nlrm: the daemon exited immediately %d times in a row (%v).\n"+
							"lrm: that is a startup failure, not Android reclaiming the process,\n"+
							"lrm: so supervision is giving up rather than looping.\n"+
							"lrm: check the error above -- common causes are running outside an\n"+
							"lrm: LRM repository, or a port already in use.\n", fastFailures, err)
					return 1
				}
			} else if err == nil || killed {
				fastFailures = 0
			}

			restarts++
			if o.maxRestarts > 0 && restarts >= o.maxRestarts {
				fmt.Fprintf(os.Stderr, "lrm: daemon exited %d times, giving up\n", restarts)
				return 1
			}
			reason := "exited"
			if err != nil {
				reason = err.Error()
			}
			if killed {
				reason = "killed by Android (out of memory / background limit)"
			}
			fmt.Fprintf(os.Stderr,
				"\nmobile: daemon %s after %s — restarting in %s (restart #%d)\n",
				reason, ran.Round(time.Second), backoff, restarts)

			select {
			case <-stop:
				return 0
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}
	}
}

// isProbablyOOMKill recognises the signals Android uses when it reclaims a
// background process.
func isProbablyOOMKill(err error) bool {
	if err == nil {
		return false
	}
	var ee *exec.ExitError
	if !asExitError(err, &ee) {
		return strings.Contains(err.Error(), "killed")
	}
	if st, ok := ee.Sys().(syscall.WaitStatus); ok && st.Signaled() {
		switch st.Signal() {
		case syscall.SIGKILL, syscall.SIGTERM:
			return true
		}
	}
	return false
}

func asExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}
