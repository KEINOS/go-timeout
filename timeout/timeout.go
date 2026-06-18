// Package timeout provides a GNU-compatible timeout command.
package timeout

import (
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
	// ExitSuccess means the command completed successfully.
	ExitSuccess = 0
	// ExitTimedOut means the command timed out.
	ExitTimedOut = 124
	// ExitInternalFailure means timeout itself failed.
	ExitInternalFailure = 125
	// ExitCannotInvoke means the command was found but could not run.
	ExitCannotInvoke = 126
	// ExitNotFound means the command was not found.
	ExitNotFound = 127
)

const (
	defaultVersion     = "(devel)"
	hoursPerDay        = 24
	maxDuration        = time.Duration(math.MaxInt64)
	optionArgumentStep = 2
	signalExitCodeBase = 128
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

// ErrUsage identifies an invalid command-line argument.
var ErrUsage = errors.New("usage error")

// basePropagationSignals lists signals that timeout forwards to the command.
var basePropagationSignals = []syscall.Signal{
	syscall.SIGHUP,
	syscall.SIGINT,
	syscall.SIGQUIT,
	syscall.SIGTERM,
}

// Mockable function variables for testing.
var (
	// debugReadBuildInfo can be replaced in tests.
	debugReadBuildInfo = debug.ReadBuildInfo

	// exitErrorWaitStatus gets the platform status from an exit error.
	// Tests can replace it to check the fallback in waitExitCode.
	exitErrorWaitStatus = func(exitErr *exec.ExitError) (syscall.WaitStatus, bool) {
		status, ok := exitErr.Sys().(syscall.WaitStatus)

		return status, ok
	}

	// signalIgnored reports whether the process inherited sig as ignored.
	// Tests can replace it without changing the process-wide signal state.
	signalIgnored = func(sig syscall.Signal) bool {
		return signal.Ignored(sig)
	}

	// signalNotify can be replaced in tests to avoid changing process-wide handlers.
	signalNotify = func(channel chan<- os.Signal, signals ...os.Signal) {
		signal.Notify(channel, signals...)
	}
)

// Config contains the parsed options, duration, and command.
type Config struct {
	Command        []string
	Duration       time.Duration
	KillAfter      time.Duration
	Signal         syscall.Signal
	Foreground     bool
	PreserveStatus bool
	ShowHelp       bool
	ShowVersion    bool
	Verbose        bool
}

// Streams contains the input and output streams used by Run.
type Streams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

/* Functions */

// HelpText returns the help text for the timeout command.
func HelpText() string {
	return `Usage:
  timeout [OPTION]... DURATION COMMAND [ARG]...

timeout starts COMMAND, and kill it if still running after DURATION.

Options:
  -f, --foreground           keep COMMAND in the foreground
  -h, --help                 display this help and exit
  -k, --kill-after=DURATION  send KILL if COMMAND is still running later
  -p, --preserve-status      preserve COMMAND status after timeout
  -s, --signal=SIGNAL        specify signal to send on timeout
  -v, --verbose              diagnose sent signals
      --version              output version information and exit

Note:
  By default, timeout sends the TERM signal upon timeout. Use --signal
  to specify a different signal, or use --kill-after to send KILL if
  the command does not terminate after the initial signal.

Examples:
  # Sends SIGTERM after 5 seconds
  timeout 5s sleep 10

  # Sends SIGKILL after 5 seconds
  timeout --signal=KILL 5s long-running-command

  # Sends SIGTERM after 5 seconds, then SIGKILL after 2 more seconds
  timeout --kill-after=2s 5s command-that-may-ignore-term
`
}

// Parse reads timeout command-line arguments and returns their configuration.
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

// ParseDuration reads a duration in GNU timeout format.
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

// ParseSignal reads a signal name, a SIG-prefixed name, or a signal number.
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

// Run runs timeout with the given arguments and streams.
// It returns the command's exit code or a timeout exit code.
func Run(args []string, streams Streams) int {
	streams = fillDefaultStreams(streams)

	config, err := Parse(args)
	if err != nil {
		printUsageError(streams.Stderr, err)

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

/* Helper/Private functions */

type lockedWriter struct {
	mutex  *sync.Mutex
	writer io.Writer
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

func appendSignalIfMissing(signals []os.Signal, sig syscall.Signal) []os.Signal {
	for _, existing := range signals {
		if existing == sig {
			return signals
		}
	}

	return append(signals, sig)
}

// buildPropagationSignals returns the signals that timeout should forward.
//
// It skips inherited ignored signals, but always includes the timeout signal
// when one is set. It also removes duplicate signals.
func buildPropagationSignals(
	timeoutSignal syscall.Signal,
	isIgnored func(syscall.Signal) bool,
) []os.Signal {
	signals := make([]os.Signal, 0, len(basePropagationSignals)+1)

	for _, sig := range basePropagationSignals {
		if sig != timeoutSignal && isIgnored(sig) {
			continue
		}

		signals = appendSignalIfMissing(signals, sig)
	}

	if timeoutSignal != 0 {
		signals = appendSignalIfMissing(signals, timeoutSignal)
	}

	return signals
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

func isOperandStart(arg string) bool {
	return arg == "-" || !strings.HasPrefix(arg, "-")
}

func isPositiveFloatOverflow(err error, value float64) bool {
	var numberError *strconv.NumError
	if !errors.As(err, &numberError) {
		return false
	}

	return errors.Is(numberError.Err, strconv.ErrRange) && math.IsInf(value, 1)
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
	// Stop the timer before it can fire. In Go 1.23 and later, Stop also makes
	// sure that no timer value remains in the channel.
	timer := time.NewTimer(time.Hour)
	timer.Stop()

	return timer
}

func newTimer(duration time.Duration) *time.Timer {
	if duration == 0 {
		return newStoppedTimer()
	}

	return time.NewTimer(duration)
}

func notifySignals(signalCh chan<- os.Signal, timeoutSignal syscall.Signal) {
	signals := buildPropagationSignals(timeoutSignal, signalIgnored)
	if len(signals) == 0 {
		return
	}

	signalNotify(signalCh, signals...)
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

func parseKillAfter(config *Config, value string) error {
	duration, err := ParseDuration(value)
	if err != nil {
		return fmt.Errorf("invalid kill-after duration %q: %w", value, err)
	}

	config.KillAfter = duration

	return nil
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

func parseOption(config *Config, args []string, index int) (int, error) {
	if strings.HasPrefix(args[index], "--") {
		return parseLongOption(config, args, index)
	}

	return parseShortOptions(config, args, index)
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

func parseShortFlag(config *Config, option byte) bool {
	switch option {
	case 'f':
		config.Foreground = true
	case 'h':
		config.ShowHelp = true
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

func parseSignalOption(config *Config, value string) error {
	parsed, err := ParseSignal(value)
	if err != nil {
		return fmt.Errorf("invalid signal %q: %w", value, err)
	}

	config.Signal = parsed

	return nil
}

func printUsageError(stderr io.Writer, err error) {
	_, _ = fmt.Fprintf(stderr, "error timeout: %v\n", err)
	_, _ = fmt.Fprintf(stderr, "\n%s", HelpText())
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

func shouldExitAfterParsing(config *Config) bool {
	return config.ShowHelp || config.ShowVersion
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
