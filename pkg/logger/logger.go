// Package logger provides basic logging functionalities.
package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"
)

// Logger defines a simple interface for logging.
type Logger interface {
	Debug(args ...interface{})
	Debugf(format string, args ...interface{})
	Info(args ...interface{})
	Infof(format string, args ...interface{})
	Warn(args ...interface{})
	Warnf(format string, args ...interface{})
	Error(args ...interface{})
	Errorf(format string, args ...interface{})
	Fatal(args ...interface{})
	Fatalf(format string, args ...interface{})
}

// defaultLogger is a simple logger implementation using the standard log package.
type defaultLogger struct {
	debugLogger *log.Logger
	infoLogger  *log.Logger
	warnLogger  *log.Logger
	errorLogger *log.Logger
	fatalLogger *log.Logger
	prefix      string
}

// NewLogger creates and new Logger instance, and updates the global `std` logger.
// loglevel could be "debug", "info", "warn", "error", "fatal"
func NewLogger(logLevel string) Logger {
	debugHandle := io.Discard
	infoHandle := io.Discard
	warnHandle := io.Discard
	errorHandle := io.Discard
	fatalHandle := os.Stderr // Fatal logs always go to stderr

	switch logLevel {
	case "debug":
		debugHandle = os.Stderr
		infoHandle = os.Stderr
		warnHandle = os.Stderr
		errorHandle = os.Stderr
	case "info":
		infoHandle = os.Stderr
		warnHandle = os.Stderr
		errorHandle = os.Stderr
	case "warn":
		warnHandle = os.Stderr
		errorHandle = os.Stderr
	case "error":
		errorHandle = os.Stderr
	}

	return &defaultLogger{
		debugLogger: log.New(debugHandle, "DEBUG: ", log.Ldate|log.Ltime|log.Lshortfile),
		infoLogger:  log.New(infoHandle, "INFO:  ", log.Ldate|log.Ltime|log.Lshortfile),
		warnLogger:  log.New(warnHandle, "WARN:  ", log.Ldate|log.Ltime|log.Lshortfile),
		errorLogger: log.New(errorHandle, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile),
		fatalLogger: log.New(fatalHandle, "FATAL: ", log.Ldate|log.Ltime|log.Lshortfile),
		prefix:      "",
	}
}

func (l *defaultLogger) Debug(args ...interface{}) {
	_ = l.debugLogger.Output(2, l.prefix+fmt.Sprintln(args...))
}

func (l *defaultLogger) Debugf(format string, args ...interface{}) {
	_ = l.debugLogger.Output(2, l.prefix+fmt.Sprintf(format, args...))
}

func (l *defaultLogger) Info(args ...interface{}) {
	_ = l.infoLogger.Output(2, l.prefix+fmt.Sprintln(args...))
}

func (l *defaultLogger) Infof(format string, args ...interface{}) {
	_ = l.infoLogger.Output(2, l.prefix+fmt.Sprintf(format, args...))
}

func (l *defaultLogger) Warn(args ...interface{}) {
	_ = l.warnLogger.Output(2, l.prefix+fmt.Sprintln(args...))
}

func (l *defaultLogger) Warnf(format string, args ...interface{}) {
	_ = l.warnLogger.Output(2, l.prefix+fmt.Sprintf(format, args...))
}

func (l *defaultLogger) Error(args ...interface{}) {
	_ = l.errorLogger.Output(2, l.prefix+fmt.Sprintln(args...))
}

func (l *defaultLogger) Errorf(format string, args ...interface{}) {
	_ = l.errorLogger.Output(2, l.prefix+fmt.Sprintf(format, args...))
}

func (l *defaultLogger) Fatal(args ...interface{}) {
	_ = l.fatalLogger.Output(2, l.prefix+fmt.Sprintln(args...))
	os.Exit(1)
}

func (l *defaultLogger) Fatalf(format string, args ...interface{}) {
	_ = l.fatalLogger.Output(2, l.prefix+fmt.Sprintf(format, args...))
	os.Exit(1)
}

var (
	std      Logger
	logMutex sync.Mutex
)

// init initializes the global logger with a default info level.
func init() {
	std = NewLogger("info")
}

// SetGlobalLogLevel reconfigures the global std logger's level safely.
func SetGlobalLogLevel(logLevel string) {
	logMutex.Lock()
	defer logMutex.Unlock()
	std = NewLogger(logLevel)
}

// Debug logs a debug message using the global std logger.
func Debug(args ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()
	std.Debug(args...)
}

// Debugf logs a debug message with formatting.
func Debugf(format string, args ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()
	std.Debugf(format, args...)
}

// Info logs an informational message using the global std logger.
func Info(args ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()
	std.Info(args...)
}

// Infof logs an informational message with formatting.
func Infof(format string, args ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()
	std.Infof(format, args...)
}

// Warn logs a warning message.
func Warn(args ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()
	std.Warn(args...)
}

// Warnf logs a warning message with formatting.
func Warnf(format string, args ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()
	std.Warnf(format, args...)
}

// Error logs an error message.
func Error(args ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()
	std.Error(args...)
}

// Errorf logs an error message with formatting.
func Errorf(format string, args ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()
	std.Errorf(format, args...)
}

// Fatal logs a fatal error message and exits.
func Fatal(args ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()
	std.Fatal(args...)
}

// Fatalf logs a fatal error message with formatting and exits.
func Fatalf(format string, args ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()
	std.Fatalf(format, args...)
}
