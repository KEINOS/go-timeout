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
	Command        []string
	Duration       time.Duration
	KillAfter      time.Duration
	Signal         syscall.Signal
	Foreground     bool
	PreserveStatus bool
	Verbose        bool
	ShowHelp       bool
	ShowVersion    bool
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

type groupSignalFunc func(int, syscall.Signal)

type processSignalFunc func(*exec.Cmd, syscall.Signal)

type runnerState struct {
	streams       Streams
	suppressBy    time.Time
	signalGroup   groupSignalFunc
	signalProcess processSignalFunc
	config        Config
	pgid          int
	suppressed    syscall.Signal
	timedOut      bool
	killSent      bool
	signalSent    bool
}

// Parse parses timeout command-line arguments.
func Parse(args []string) (Config, error) {
	config := new(Config)
	config.Signal = syscall.SIGTERM

	index, err := parseOptions(config, args)
	if err != nil {
		return Config{}, err
	}

	if shouldExitAfterParsing(config) {
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

	number, multiplier, err := splitDuration(value)
	if err != nil {
		return 0, err
	}

	parsed, err := parseDurationNumber(number)
	if err != nil {
		return 0, usageError("invalid duration number")
	}

	if parsed < 0 {
		return 0, usageError("invalid duration number")
	}

	if math.IsInf(parsed, 1) {
		return maxDuration, nil
	}

	nanoseconds := parsed * float64(multiplier)
	if nanoseconds > float64(maxDuration) {
		return maxDuration, nil
	}

	return time.Duration(nanoseconds), nil
}

func splitDuration(value string) (string, time.Duration, error) {
	lastRune := rune(value[len(value)-1])
	if !unicode.IsLetter(lastRune) {
		return value, time.Second, nil
	}

	multiplier, err := durationMultiplier(lastRune)
	if err != nil {
		return "", 0, err
	}

	return value[:len(value)-1], multiplier, nil
}

func durationMultiplier(suffix rune) (time.Duration, error) {
	switch suffix {
	case 's':
		return time.Second, nil
	case 'm':
		return time.Minute, nil
	case 'h':
		return time.Hour, nil
	case 'd':
		return hoursPerDay * time.Hour, nil
	default:
		return 0, usageError("invalid duration suffix")
	}
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
			return state.timeoutExitCode(err)
		case <-initialTimer.C:
			state.timedOut = true
			state.sendSignal(cmd, state.config.Signal)
			state.startKillTimer(killTimer)
		case <-killTimer.C:
			state.killSent = true
			state.sendSignal(cmd, syscall.SIGKILL)
		case received := <-signalCh:
			state.forwardSignal(cmd, received, killTimer)
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
		state.sendProcessSignal(cmd, sig)

		return
	}

	// GNU timeout sends the signal both to the monitored process and to the
	// shared process group. The direct signal covers edge cases where the command
	// has changed process groups; SIGCONT below follows the same policy.
	state.sendProcessSignal(cmd, sig)
	state.sendGroupSignal(sig)
	state.suppressed = sig
	state.suppressBy = time.Now().Add(signalSuppressionDuration)

	if shouldResumeAfterSignal(sig) {
		state.sendProcessSignal(cmd, syscall.SIGCONT)
		state.sendGroupSignal(syscall.SIGCONT)
	}
}

func (state *runnerState) sendProcessSignal(cmd *exec.Cmd, sig syscall.Signal) {
	signalProcess := state.signalProcess
	if signalProcess == nil {
		signalProcess = defaultProcessSignal
	}

	signalProcess(cmd, sig)
}

func (state *runnerState) sendGroupSignal(sig syscall.Signal) {
	signalGroup := state.signalGroup
	if signalGroup == nil {
		signalGroup = defaultGroupSignal
	}

	signalGroup(state.pgid, sig)
}

func (state *runnerState) startErrorExitCode(err error) int {
	exitCode := classifyStartError(err)

	_, _ = fmt.Fprintf(state.streams.Stderr, "timeout: failed to run command %q: %v\n", state.config.Command[0], err)

	return exitCode
}

func classifyStartError(err error) int {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return ExitNotFound
	}

	if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ENOEXEC) {
		return ExitCannotInvoke
	}

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

func (state *runnerState) timeoutExitCode(err error) int {
	exitCode := state.waitExitCode(err)
	if state.timedOut && !state.config.PreserveStatus && exitCode != signalExitCode(syscall.SIGKILL) {
		return ExitTimedOut
	}

	return exitCode
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

func (state *runnerState) forwardSignal(cmd *exec.Cmd, received os.Signal, killTimer *time.Timer) {
	sig, ok := received.(syscall.Signal)
	if !ok {
		return
	}

	if state.shouldSuppress(sig) {
		return
	}

	state.sendSignal(cmd, sig)
	state.startKillTimer(killTimer)
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

func parseDurationNumber(number string) (float64, error) {
	parsed, err := strconv.ParseFloat(number, 64)
	if err != nil && !isPositiveFloatOverflow(err, parsed) {
		return 0, fmt.Errorf("parse duration number: %w", err)
	}

	if math.IsNaN(parsed) {
		return 0, usageError("invalid duration number")
	}

	return parsed, nil
}

func defaultGroupSignal(pgid int, sig syscall.Signal) {
	// GNU timeout treats signal delivery as best effort; the command may have
	// already exited or changed process groups by the time the signal is sent.
	_ = syscall.Kill(-pgid, sig)
}

func defaultProcessSignal(cmd *exec.Cmd, sig syscall.Signal) {
	// GNU timeout treats signal delivery as best effort; the command may have
	// already exited by the time the signal is sent.
	_ = cmd.Process.Signal(sig)
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

func parseOptions(config *Config, args []string) (int, error) {
	index := 0
	for index < len(args) {
		arg := args[index]
		if arg == "--" {
			return index + 1, nil
		}

		if isOperandStart(arg) {
			return index, nil
		}

		nextIndex, err := parseOption(config, args, index)
		if err != nil {
			return 0, err
		}

		index = nextIndex
	}

	return index, nil
}

func shouldExitAfterParsing(config *Config) bool {
	return config.ShowHelp || config.ShowVersion
}

func parseOption(config *Config, args []string, index int) (int, error) {
	if strings.HasPrefix(args[index], "--") {
		return parseLongOption(config, args, index)
	}

	return parseShortOptions(config, args, index)
}

func isOperandStart(arg string) bool {
	return arg == "-" || !strings.HasPrefix(arg, "-")
}

func parseLongOption(config *Config, args []string, index int) (int, error) {
	arg := args[index]

	if parseLongFlag(config, arg) {
		return index + 1, nil
	}

	if value, ok := strings.CutPrefix(arg, optionKillAfter+"="); ok {
		return index + 1, parseKillAfter(config, value)
	}

	if value, ok := strings.CutPrefix(arg, optionSignal+"="); ok {
		return index + 1, parseSignalOption(config, value)
	}

	return parseLongOptionArgument(config, args, index)
}

func parseLongFlag(config *Config, arg string) bool {
	switch arg {
	case optionForeground:
		config.Foreground = true
	case optionPreserveStatus:
		config.PreserveStatus = true
	case optionVerbose:
		config.Verbose = true
	case optionHelp:
		config.ShowHelp = true
	case optionVersion:
		config.ShowVersion = true
	default:
		return false
	}

	return true
}

func parseLongOptionArgument(config *Config, args []string, index int) (int, error) {
	switch args[index] {
	case optionKillAfter:
		value, nextIndex, err := requireOptionArgument(args, index, optionKillAfter)
		if err != nil {
			return 0, err
		}

		return nextIndex, parseKillAfter(config, value)
	case optionSignal:
		value, nextIndex, err := requireOptionArgument(args, index, optionSignal)
		if err != nil {
			return 0, err
		}

		return nextIndex, parseSignalOption(config, value)
	default:
		return 0, usageError("unrecognized option " + args[index])
	}
}

func parseShortOptions(config *Config, args []string, index int) (int, error) {
	arg := args[index]

	for offset := 1; offset < len(arg); offset++ {
		if parseShortFlag(config, arg[offset]) {
			continue
		}

		return parseShortOptionArgument(config, args, index, offset)
	}

	return index + 1, nil
}

func parseShortFlag(config *Config, option byte) bool {
	switch option {
	case 'f':
		config.Foreground = true
	case 'p':
		config.PreserveStatus = true
	case 'v':
		config.Verbose = true
	default:
		return false
	}

	return true
}

func parseShortOptionArgument(config *Config, args []string, index int, offset int) (int, error) {
	arg := args[index]
	value := arg[offset+1:]

	switch arg[offset] {
	case 'k':
		return parseShortOptionValue(config, args, index, value, "-k", parseKillAfter)
	case 's':
		return parseShortOptionValue(config, args, index, value, "-s", parseSignalOption)
	default:
		return 0, usageError("invalid option " + string(arg[offset]))
	}
}

func parseShortOptionValue(
	config *Config,
	args []string,
	index int,
	value string,
	option string,
	parseValue func(*Config, string) error,
) (int, error) {
	if value != "" {
		return index + 1, parseValue(config, value)
	}

	nextValue, nextIndex, err := requireOptionArgument(args, index, option)
	if err != nil {
		return 0, err
	}

	return nextIndex, parseValue(config, nextValue)
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

func shouldResumeAfterSignal(sig syscall.Signal) bool {
	return sig != syscall.SIGKILL && sig != syscall.SIGCONT
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
