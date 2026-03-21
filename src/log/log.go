// Copyright 2018 YogoRobot Inc. All rights reserved.

package log

import (
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"sync"
	"time"
)

// Level
const (
	DEBUG int = iota
	INFO
	WARN
	ERROR
)

// default
const (
	DefaultCallerDepth = 2
)

type StdoutWriter struct {
	w io.Writer
}

func NewStdoutWriter() *StdoutWriter {
	return &StdoutWriter{w: os.Stdout}
}

func (w *StdoutWriter) Write(p []byte) (int, error) {
	return fmt.Fprintf(w.w, "%s\n", string(p))
}

// Global registry
var (
	m                       = sync.Mutex{}
	registry                = make(map[string]*Logger, 0)
	defaultWriter io.Writer = NewStdoutWriter()
)

// if w is nil pointer, the default writer will reset to stdout.
func SetDefaultWriter(w io.Writer) {
	if w != nil {
		defaultWriter = w
	} else {
		defaultWriter = NewStdoutWriter()
	}
}

// Level name
var levelNames = [4]string{"D", "I", "W", "E"}

// Logger abstraction.
type Logger struct {
	name                      string
	level                     int
	w                         io.Writer
	colored                   bool
	enabled                   bool
	prefix                    string
	callerDepth               int
	enableCallerSourceLogging bool
}

// New creates a new Logger.
func Get(name string) *Logger {
	m.Lock()
	defer m.Unlock()
	l, ok := registry[name]
	if ok {
		return l
	}
	l = &Logger{
		name:                      name,
		level:                     DEBUG,
		colored:                   true,
		enabled:                   true,
		callerDepth:               DefaultCallerDepth,
		enableCallerSourceLogging: true,
	}
	registry[name] = l
	return l
}

var Log *Logger

func init() {
	Log = Get("")
}

// Get a logger with prefix.
func GetWithPrefix(name string, prefix string) *Logger {
	l := Get(name)
	l.SetPrefix(prefix)
	return l
}

// colors to ansi code map
var colors = map[string]int{
	"black":   0,
	"red":     1,
	"green":   2,
	"yellow":  3,
	"blue":    4,
	"magenta": 5,
	"cyan":    6,
	"white":   7,
}

// levelColors
var levelColors = map[int]string{
	DEBUG: "blue",
	INFO:  "green",
	WARN:  "magenta",
	ERROR: "red",
}

func (l *Logger) SetName(name string) {
	l.name = name
}

// SetColored sets the color enability.
func (l *Logger) SetColored(b bool) {
	l.colored = b
}

// SetLevel sets the logging level.
func (l *Logger) SetLevel(level int) {
	l.level = level % len(levelNames)
}

// SetWriter sets the writer.
func (l *Logger) SetWriter(w io.Writer) {
	l.w = w
}

// SetPrefix sets the prefix for this logger.
func (l *Logger) SetPrefix(prefix string) {
	l.prefix = prefix
}

// SetCallerDepth sets the caller depth for this logger.
func (l *Logger) SetCallerDepth(callerDepth int) {
	l.callerDepth = callerDepth
}

// DisableCallerSourceLogging disables the logging for caller source.
// This sets to true by default.
func (l *Logger) DisableCallerSourceLogging() {
	l.enableCallerSourceLogging = false
}

// Disable the logging.
func (l *Logger) Disable() {
	l.enabled = false
}

// Enable the logging.
func (l *Logger) Enable() {
	l.enabled = true
}

// Debug logs message with level DEBUG.
func (l *Logger) Debug(a ...interface{}) {}
func Debug(a ...interface{})             { Log.log(DEBUG, fmt.Sprint(a...)) }

// Info logs message with level INFO.
func (l *Logger) Info(a ...interface{}) { l.log(INFO, fmt.Sprint(a...)) }
func Info(a ...interface{})             { Log.log(INFO, fmt.Sprint(a...)) }

// Warn logs message with level WARN.
func (l *Logger) Warn(a ...interface{}) { l.log(WARN, fmt.Sprint(a...)) }
func Warn(a ...interface{})             { Log.log(WARN, fmt.Sprint(a...)) }

// Error logs message with level ERROR.
func (l *Logger) Error(a ...interface{}) { l.log(ERROR, fmt.Sprint(a...)) }
func Error(a ...interface{})             { Log.log(ERROR, fmt.Sprint(a...)) }

// Fatal and logs message with level FATAL.
func (l *Logger) Fatal(a ...interface{}) {
	d := fmt.Sprint(a...)
	l.log(ERROR, d)
	panic(d)
}
func Fatal(a ...interface{}) {
	d := fmt.Sprint(a...)
	Log.log(ERROR, d)
	panic(d)
}

// Debugf formats and logs message with level DEBUG.
func (l *Logger) Debugf(format string, a ...interface{}) {}
func Debugf(format string, a ...interface{}) {
	Log.log(DEBUG, fmt.Sprintf(format, a...))
}

// Infof formats and logs message with level INFO.
func (l *Logger) Infof(format string, a ...interface{}) {
	l.log(INFO, fmt.Sprintf(format, a...))
}
func Infof(format string, a ...interface{}) {
	Log.log(INFO, fmt.Sprintf(format, a...))
}

// Warnf formats and logs message with level WARN.
func (l *Logger) Warnf(format string, a ...interface{}) {
	l.log(WARN, fmt.Sprintf(format, a...))
}
func Warnf(format string, a ...interface{}) {
	Log.log(WARN, fmt.Sprintf(format, a...))
}

// Errorf formats and logs message with level ERROR.
func (l *Logger) Errorf(format string, a ...interface{}) {
	l.log(ERROR, fmt.Sprintf(format, a...))
}
func Errorf(format string, a ...interface{}) {
	Log.log(ERROR, fmt.Sprintf(format, a...))
}

// Fatalf formats and logs message with level FATAL.
func (l *Logger) Fatalf(format string, a ...interface{}) {
	d := fmt.Sprintf(format, a...)
	l.log(ERROR, d)
	panic(d)
}
func Fatalf(format string, a ...interface{}) {
	d := fmt.Sprintf(format, a...)
	Log.log(ERROR, d)
	panic(d)
}

// Colored returns text in color.
func Colored(color string, text string) string {
	return fmt.Sprintf("\033[3%dm%s\033[0m", colors[color], text)
}

// log dose logging.
func (l *Logger) log(level int, msg string) error {
	if l.enabled && level >= l.level {
		// Caller pkg.
		_, fileName, line, _ := runtime.Caller(l.callerDepth)
		// pc, fileName, line, _ := runtime.Caller(l.callerDepth)
		// fullName := runtime.FuncForPC(pc).Name()
		// parts := strings.Split(fullName, ".")
		// funcName :=  parts[len(parts)-1]
		pkgName := path.Base(path.Dir(fileName))
		filepath := path.Join(pkgName, path.Base(fileName))
		// Datetime and pid.
		now := time.Now()
		fmtNow := now.Format("2006-01-02 15:04:05.000000")
		// Message
		levelName := levelNames[level]
		// Whether to log the caller source.
		var headerString string
		if !l.enableCallerSourceLogging {
			headerString = fmt.Sprintf("[%s] %s %s", l.name, levelName, fmtNow)
		} else {
			headerString = fmt.Sprintf("%s %s %s:%d ", fmtNow, Colored(levelColors[level], levelName), filepath, line)
			// headerString = fmt.Sprintf("%s %s %s:%d %s", fmtNow,  Colored(levelColors[level], levelName), filepath, line, funcName)
		}
		//header := Colored(levelColors[level], headerString)
		header := headerString
		msg = Colored(levelColors[level], msg)
		if l.prefix != "" {
			msg = fmt.Sprintf("%s %s", l.prefix, msg)
		}
		var writer io.Writer
		if l.w != nil {
			writer = l.w
		} else {
			writer = defaultWriter
		}
		_, err := fmt.Fprintf(writer, "%s %s", header, msg)
		return err
	}
	return nil
}
