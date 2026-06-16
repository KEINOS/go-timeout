/*
Package timeout implements a GNU-compatible timeout command.
*/
package timeout

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

const (
	// ExitSuccess is the exit code used for successful execution.
	ExitSuccess = 0
	// ExitTimedOut is the exit code used when the command times out.
	ExitTimedOut = 124
	// ExitInternalFailure is the exit code used for timeout's own failures.
	ExitInternalFailure = 125
	// ExitCannotInvoke is the exit code used when a command cannot be invoked.
	ExitCannotInvoke = 126
	// ExitNotFound is the exit code used when a command cannot be found.
	ExitNotFound = 127
)

const (
	defaultVersion            = "(devel)"
	hoursPerDay               = 24
	maxDuration               = time.Duration(math.MaxInt64)
	optionArgumentStep        = 2
	signalExitCodeBase        = 128
	signalSuppressionDuration = 200 * time.Millisecond
)

const (
	optionForeground     = "--foreground"
	optionHelp           = "--help"
	optionKillAfter      = "--kill-after"
	optionPreserveStatus = "--preserve-status"
	optionSignal         = "--signal"
	optionVerbose        = "--verbose"
	optionVersion        = "--version"
)

// ErrUsage marks command-line usage errors.
var ErrUsage = errors.New("usage error")

var debugReadBuildInfo = debug.ReadBuildInfo

var supportedSignals = map[string]syscall.Signal{
	"ABRT": syscall.SIGABRT,
	"ALRM": syscall.SIGALRM,
	"CONT": syscall.SIGCONT,
	"HUP":  syscall.SIGHUP,
	"ILL":  syscall.SIGILL,
	"INT":  syscall.SIGINT,
	"KILL": syscall.SIGKILL,
	"PIPE": syscall.SIGPIPE,
	"QUIT": syscall.SIGQUIT,
	"STOP": syscall.SIGSTOP,
	"TERM": syscall.SIGTERM,
	"TSTP": syscall.SIGTSTP,
	"TTIN": syscall.SIGTTIN,
	"TTOU": syscall.SIGTTOU,
	"USR1": syscall.SIGUSR1,
	"USR2": syscall.SIGUSR2,
}

// Config contains parsed timeout options and operands.
type Config struct {
	Foreground     bool
	PreserveStatus bool
	Verbose        bool
	ShowHelp       bool
	ShowVersion    bool
	Duration       time.Duration
	KillAfter      time.Duration
	Signal         syscall.Signal
	Command        []string
}

// Streams contains standard streams used by Run.
type Streams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type lockedWriter struct {
	mutex  *sync.Mutex
	writer io.Writer
}

type runnerState struct {
	config     Config
	streams    Streams
	pgid       int
	timedOut   bool
	killSent   bool
	signalSent bool
	suppressed syscall.Signal
	suppressBy time.Time
}

// Parse parses timeout command-line arguments.
func Parse(args []string) (Config, error) {
	config := new(Config)
	config.Signal = syscall.SIGTERM

	index := 0
	for index < len(args) {
		arg := args[index]
		if arg == "--" {
			index++

			break
		}

		if arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}

		if strings.HasPrefix(arg, "--") {
			nextIndex, err := parseLongOption(config, args, index)
			if err != nil {
				return Config{}, err
			}

			index = nextIndex

			continue
		}

		nextIndex, err := parseShortOptions(config, args, index)
		if err != nil {
			return Config{}, err
		}

		index = nextIndex
	}

	if config.ShowHelp || config.ShowVersion {
		return *config, nil
	}

	if index >= len(args) {
		return Config{}, usageError("missing operand")
	}

	if index+1 >= len(args) {
		return Config{}, usageError("missing command")
	}

	duration, err := ParseDuration(args[index])
	if err != nil {
		return Config{}, fmt.Errorf("invalid duration %q: %w", args[index], err)
	}

	config.Duration = duration

	config.Command = append([]string(nil), args[index+1:]...)

	return *config, nil
}

// ParseDuration parses GNU timeout duration syntax.
func ParseDuration(value string) (time.Duration, error) {
	if value == "" {
		return 0, usageError("empty duration")
	}

	multiplier := time.Second
	number := value
	lastRune := rune(value[len(value)-1])

	if unicode.IsLetter(lastRune) {
		number = value[:len(value)-1]

		switch lastRune {
		case 's':
			multiplier = time.Second
		case 'm':
			multiplier = time.Minute
		case 'h':
			multiplier = time.Hour
		case 'd':
			multiplier = hoursPerDay * time.Hour
		default:
			return 0, usageError("invalid duration suffix")
		}
	}

	parsed, err := strconv.ParseFloat(number, 64)
	if err != nil && !isPositiveFloatOverflow(err, parsed) {
		return 0, usageError("invalid duration number")
	}

	if math.IsNaN(parsed) || parsed < 0 {
		return 0, usageError("invalid duration number")
	}

	if math.IsInf(parsed, 1) {
		if err == nil {
			return 0, usageError("invalid duration number")
		}

		return maxDuration, nil
	}

	nanoseconds := parsed * float64(multiplier)
	if nanoseconds > float64(maxDuration) {
		return maxDuration, nil
	}

	return time.Duration(nanoseconds), nil
}

// ParseSignal parses a signal name, SIG-prefixed name, or signal number.
func ParseSignal(value string) (syscall.Signal, error) {
	if value == "" {
		return 0, usageError("empty signal")
	}

	number, err := strconv.Atoi(value)
	if err == nil {
		if number <= 0 {
			return 0, usageError("invalid signal number")
		}

		parsed := syscall.Signal(number)
		if !isSupportedSignal(parsed) {
			return 0, usageError("unsupported signal number")
		}

		return parsed, nil
	}

	name := strings.ToUpper(value)
	name = strings.TrimPrefix(name, "SIG")

	parsed, ok := supportedSignals[name]
	if !ok {
		return 0, usageError("unknown signal")
	}

	return parsed, nil
}

// Run runs timeout with the given arguments and streams and returns an exit code.
func Run(args []string, streams Streams) int {
	streams = fillDefaultStreams(streams)

	config, err := Parse(args)
	if err != nil {
		_, _ = fmt.Fprintf(streams.Stderr, "timeout: %v\n", err)

		return ExitInternalFailure
	}

	if config.ShowHelp {
		_, _ = fmt.Fprint(streams.Stdout, HelpText())

		return ExitSuccess
	}

	if config.ShowVersion {
		_, _ = fmt.Fprintf(streams.Stdout, "timeout %s\n", getAppVersion())

		return ExitSuccess
	}

	state := new(runnerState)
	state.config = config
	state.streams = streams

	return state.runCommand()
}

// HelpText returns the command help text.
func HelpText() string {
	return `Usage: timeout [OPTION]... DURATION COMMAND [ARG]...
Start COMMAND, and kill it if still running after DURATION.

Options:
  -f, --foreground          keep COMMAND in the foreground
  -k, --kill-after=DURATION send KILL if COMMAND is still running later
  -p, --preserve-status     preserve COMMAND status after timeout
  -s, --signal=SIGNAL       specify signal to send on timeout
  -v, --verbose             diagnose sent signals
      --help                display this help and exit
      --version             output version information and exit
`
}

// runCommand starts the user command as a child process.
// In background mode, timeout and the child share one process group.
// This lets timeout send signals to the whole command.
func (state *runnerState) runCommand() int {
	if !state.config.Foreground {
		err := syscall.Setpgid(0, 0)
		if err != nil && !errors.Is(err, syscall.EPERM) {
			_, _ = fmt.Fprintf(state.streams.Stderr, "timeout: set process group: %v\n", err)

			return ExitInternalFailure
		}

		pgid, err := syscall.Getpgid(0)
		if err != nil {
			_, _ = fmt.Fprintf(state.streams.Stderr, "timeout: get process group: %v\n", err)

			return ExitInternalFailure
		}

		state.pgid = pgid
	}

	//nolint:gosec // Running the requested command is the purpose of this package.
	cmd := exec.CommandContext(context.Background(), state.config.Command[0], state.config.Command[1:]...)
	cmd.Stdin = state.streams.Stdin
	cmd.Stdout = state.streams.Stdout
	cmd.Stderr = state.streams.Stderr

	err := cmd.Start()
	if err != nil {
		return state.startErrorExitCode(err)
	}

	return state.waitForCommand(cmd)
}

func (state *runnerState) waitForCommand(cmd *exec.Cmd) int {
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	initialTimer := newTimer(state.config.Duration)
	killTimer := newStoppedTimer()

	defer stopTimer(initialTimer)
	defer stopTimer(killTimer)

	signalCh := make(chan os.Signal, 1)
	notifySignals(signalCh, state.config.Signal)

	defer signal.Stop(signalCh)

	for {
		select {
		case err := <-done:
			exitCode := state.waitExitCode(err)
			if state.timedOut && !state.config.PreserveStatus && exitCode != signalExitCode(syscall.SIGKILL) {
				return ExitTimedOut
			}

			return exitCode
		case <-initialTimer.C:
			state.timedOut = true
			state.sendSignal(cmd, state.config.Signal)
			state.startKillTimer(killTimer)
		case <-killTimer.C:
			state.killSent = true
			state.sendSignal(cmd, syscall.SIGKILL)
		case received := <-signalCh:
			sig, ok := received.(syscall.Signal)
			if !ok {
				continue
			}

			if state.shouldSuppress(sig) {
				continue
			}

			state.sendSignal(cmd, sig)
			state.startKillTimer(killTimer)
		}
	}
}

func (state *runnerState) sendSignal(cmd *exec.Cmd, sig syscall.Signal) {
	state.signalSent = true

	if state.config.Verbose {
		_, _ = fmt.Fprintf(state.streams.Stderr, "timeout: sending signal %s to command %s\n",
			signalName(sig), state.config.Command[0])
	}

	if state.config.Foreground {
		_ = cmd.Process.Signal(sig)

		return
	}

	// GNU timeout sends the signal both to the monitored process and to the
	// shared process group. The direct signal covers edge cases where the command
	// has changed process groups; SIGCONT below follows the same policy.
	_ = cmd.Process.Signal(sig)
	_ = syscall.Kill(-state.pgid, sig)
	state.suppressed = sig
	state.suppressBy = time.Now().Add(signalSuppressionDuration)

	if sig != syscall.SIGKILL && sig != syscall.SIGCONT {
		_ = cmd.Process.Signal(syscall.SIGCONT)
		_ = syscall.Kill(-state.pgid, syscall.SIGCONT)
	}
}

func (state *runnerState) startErrorExitCode(err error) int {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintf(state.streams.Stderr, "timeout: failed to run command %q: %v\n", state.config.Command[0], err)

		return ExitNotFound
	}

	if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ENOEXEC) {
		_, _ = fmt.Fprintf(state.streams.Stderr, "timeout: failed to run command %q: %v\n", state.config.Command[0], err)

		return ExitCannotInvoke
	}

	_, _ = fmt.Fprintf(state.streams.Stderr, "timeout: failed to run command %q: %v\n", state.config.Command[0], err)

	return ExitInternalFailure
}

func (state *runnerState) shouldSuppress(sig syscall.Signal) bool {
	// Process-group signaling can echo the monitor's own signal back to the
	// monitor. Suppress only a short same-signal window: long enough to avoid a
	// self-signal loop, but intentionally small so a later external signal is
	// still propagated.
	if state.suppressed != sig {
		return false
	}

	if time.Now().After(state.suppressBy) {
		state.suppressed = 0

		return false
	}

	return true
}

func (state *runnerState) startKillTimer(timer *time.Timer) {
	if state.killSent || state.config.KillAfter == 0 {
		return
	}

	resetTimer(timer, state.config.KillAfter)
}

func (state *runnerState) waitExitCode(err error) int {
	if err == nil {
		return ExitSuccess
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		_, _ = fmt.Fprintf(state.streams.Stderr, "timeout: wait for command: %v\n", err)

		return ExitInternalFailure
	}

	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		return exitErr.ExitCode()
	}

	if status.Signaled() {
		return signalExitCode(status.Signal())
	}

	return status.ExitStatus()
}

func fillDefaultStreams(streams Streams) Streams {
	if streams.Stdin == nil {
		streams.Stdin = os.Stdin
	}

	if streams.Stdout == nil {
		streams.Stdout = os.Stdout
	}

	if streams.Stderr == nil {
		streams.Stderr = os.Stderr
	}

	streams.Stdout = lockedWriter{
		mutex:  new(sync.Mutex),
		writer: streams.Stdout,
	}
	streams.Stderr = lockedWriter{
		mutex:  new(sync.Mutex),
		writer: streams.Stderr,
	}

	return streams
}

func getAppVersion() string {
	return versionFromBuildInfo(debugReadBuildInfo())
}

func (writer lockedWriter) Write(data []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()

	written, err := writer.writer.Write(data)
	if err != nil {
		return written, fmt.Errorf("write stream: %w", err)
	}

	return written, nil
}

func isPositiveFloatOverflow(err error, value float64) bool {
	var numberError *strconv.NumError
	if !errors.As(err, &numberError) {
		return false
	}

	return errors.Is(numberError.Err, strconv.ErrRange) && math.IsInf(value, 1)
}

func appendSignalIfMissing(signals []os.Signal, sig syscall.Signal) []os.Signal {
	for _, existing := range signals {
		if existing == sig {
			return signals
		}
	}

	return append(signals, sig)
}

func isSupportedSignal(sig syscall.Signal) bool {
	for _, supported := range supportedSignals {
		if supported == sig {
			return true
		}
	}

	return false
}

func newStoppedTimer() *time.Timer {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}

	return timer
}

func newTimer(duration time.Duration) *time.Timer {
	if duration == 0 {
		return newStoppedTimer()
	}

	return time.NewTimer(duration)
}

func notifySignals(signalCh chan<- os.Signal, timeoutSignal syscall.Signal) {
	signals := []os.Signal{
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGTERM,
	}

	if timeoutSignal != 0 {
		signals = appendSignalIfMissing(signals, timeoutSignal)
	}

	signal.Notify(signalCh, signals...)
}

func parseLongOption(config *Config, args []string, index int) (int, error) {
	arg := args[index]

	switch {
	case arg == optionForeground:
		config.Foreground = true

		return index + 1, nil
	case arg == optionPreserveStatus:
		config.PreserveStatus = true

		return index + 1, nil
	case arg == optionVerbose:
		config.Verbose = true

		return index + 1, nil
	case arg == optionHelp:
		config.ShowHelp = true

		return index + 1, nil
	case arg == optionVersion:
		config.ShowVersion = true

		return index + 1, nil
	case arg == optionKillAfter:
		value, nextIndex, err := requireOptionArgument(args, index, optionKillAfter)
		if err != nil {
			return 0, err
		}

		return nextIndex, parseKillAfter(config, value)
	case strings.HasPrefix(arg, optionKillAfter+"="):
		return index + 1, parseKillAfter(config, strings.TrimPrefix(arg, optionKillAfter+"="))
	case arg == optionSignal:
		value, nextIndex, err := requireOptionArgument(args, index, optionSignal)
		if err != nil {
			return 0, err
		}

		return nextIndex, parseSignalOption(config, value)
	case strings.HasPrefix(arg, optionSignal+"="):
		return index + 1, parseSignalOption(config, strings.TrimPrefix(arg, optionSignal+"="))
	default:
		return 0, usageError("unrecognized option " + arg)
	}
}

func parseShortOptions(config *Config, args []string, index int) (int, error) {
	arg := args[index]

	for offset := 1; offset < len(arg); offset++ {
		switch arg[offset] {
		case 'f':
			config.Foreground = true
		case 'p':
			config.PreserveStatus = true
		case 'v':
			config.Verbose = true
		case 'k':
			value := arg[offset+1:]
			if value != "" {
				return index + 1, parseKillAfter(config, value)
			}

			nextValue, nextIndex, err := requireOptionArgument(args, index, "-k")
			if err != nil {
				return 0, err
			}

			return nextIndex, parseKillAfter(config, nextValue)
		case 's':
			value := arg[offset+1:]
			if value != "" {
				return index + 1, parseSignalOption(config, value)
			}

			nextValue, nextIndex, err := requireOptionArgument(args, index, "-s")
			if err != nil {
				return 0, err
			}

			return nextIndex, parseSignalOption(config, nextValue)
		default:
			return 0, usageError("invalid option " + string(arg[offset]))
		}
	}

	return index + 1, nil
}

func parseKillAfter(config *Config, value string) error {
	duration, err := ParseDuration(value)
	if err != nil {
		return fmt.Errorf("invalid kill-after duration %q: %w", value, err)
	}

	config.KillAfter = duration

	return nil
}

func parseSignalOption(config *Config, value string) error {
	parsed, err := ParseSignal(value)
	if err != nil {
		return fmt.Errorf("invalid signal %q: %w", value, err)
	}

	config.Signal = parsed

	return nil
}

func requireOptionArgument(args []string, index int, option string) (string, int, error) {
	if index+1 >= len(args) {
		return "", 0, usageError("option requires an argument " + option)
	}

	return args[index+1], index + optionArgumentStep, nil
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}

	timer.Reset(duration)
}

func signalExitCode(sig syscall.Signal) int {
	return signalExitCodeBase + int(sig)
}

func signalName(sig syscall.Signal) string {
	for name, signalValue := range supportedSignals {
		if signalValue == sig {
			return name
		}
	}

	return sig.String()
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}

	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func usageError(message string) error {
	return fmt.Errorf("%s: %w", message, ErrUsage)
}

func versionFromBuildInfo(info *debug.BuildInfo, ok bool) string {
	if ok && info != nil && info.Main.Version != "" {
		return info.Main.Version
	}

	return defaultVersion
}
