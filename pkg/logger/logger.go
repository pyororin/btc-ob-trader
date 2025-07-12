// Package logger provides basic logging functionalities.
package logger

import (
	"io"
	"log"
	"os"
)

// Logger defines a simple interface for logging.
type Logger interface {
	Debug(args ...interface{})
	Debugf(format string, args ...interface{})
	Info(args ...interface{})
	Infof(format string, args ...interface{})
	Error(args ...interface{})
	Errorf(format string, args ...interface{})
	Fatal(args ...interface{})
	Fatalf(format string, args ...interface{})
}

// defaultLogger is a simple logger implementation using the standard log package.
type defaultLogger struct {
	debugLogger *log.Logger
	infoLogger  *log.Logger
	errorLogger *log.Logger
	fatalLogger *log.Logger
}

// NewLogger creates and configures a new Logger instance, and updates the global `std` logger.
// loglevel could be "debug", "info", "warn", "error", "fatal"
func NewLogger(logLevel string) Logger {
	infoHandle := os.Stdout
	errorHandle := os.Stderr
	fatalHandle := os.Stderr // Typically fatal also goes to stderr
	debugHandle := os.Stdout

	var dLog, iLog, eLog, fLog *log.Logger

	dLog = log.New(debugHandle, "DEBUG: ", log.Ldate|log.Ltime|log.Lshortfile)
	iLog = log.New(infoHandle, "INFO:  ", log.Ldate|log.Ltime|log.Lshortfile)
	eLog = log.New(errorHandle, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
	fLog = log.New(fatalHandle, "FATAL: ", log.Ldate|log.Ltime|log.Lshortfile)

	if logLevel != "debug" {
		dLog = log.New(io.Discard, "", 0)
	}
	if logLevel == "error" || logLevel == "fatal" {
		iLog = log.New(io.Discard, "", 0)
	}
	if logLevel == "fatal" {
		eLog = log.New(io.Discard, "", 0)
	}

	return &defaultLogger{
		debugLogger: dLog,
		infoLogger:  iLog,
		errorLogger: eLog,
		fatalLogger: fLog,
	}
}

func (l *defaultLogger) Debug(args ...interface{}) {
	l.debugLogger.Println(args...)
}

func (l *defaultLogger) Debugf(format string, args ...interface{}) {
	l.debugLogger.Printf(format, args...)
}

func (l *defaultLogger) Info(args ...interface{}) {
	l.infoLogger.Println(args...)
}

func (l *defaultLogger) Infof(format string, args ...interface{}) {
	l.infoLogger.Printf(format, args...)
}

func (l *defaultLogger) Error(args ...interface{}) {
	l.errorLogger.Println(args...)
}

func (l *defaultLogger) Errorf(format string, args ...interface{}) {
	l.errorLogger.Printf(format, args...)
}

func (l *defaultLogger) Fatal(args ...interface{}) {
	l.fatalLogger.Fatalln(args...)
}

func (l *defaultLogger) Fatalf(format string, args ...interface{}) {
	l.fatalLogger.Fatalf(format, args...)
}

// Global std logger instance, initialized directly with default "info" settings.
var std Logger = &defaultLogger{
	debugLogger: log.New(io.Discard, "", 0), // Debug is off by default
	infoLogger:  log.New(os.Stdout, "INFO:  ", log.Ldate|log.Ltime|log.Lshortfile),
	errorLogger: log.New(os.Stderr, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile),
	fatalLogger: log.New(os.Stderr, "FATAL: ", log.Ldate|log.Ltime|log.Lshortfile),
}

// SetGlobalLogLevel reconfigures the global std logger's level.
func SetGlobalLogLevel(logLevel string) {
	var debugHandle io.Writer = io.Discard
	var infoHandle io.Writer = os.Stdout
	var errorHandle io.Writer = os.Stderr

	switch logLevel {
	case "debug":
		debugHandle = os.Stdout
	case "info":
		// infoHandle is already os.Stdout
	case "error", "fatal":
		infoHandle = io.Discard
	}

	if logLevel == "fatal" {
		errorHandle = io.Discard
	}

	if globalStd, ok := std.(*defaultLogger); ok {
		globalStd.debugLogger = log.New(debugHandle, "DEBUG: ", log.Ldate|log.Ltime|log.Lshortfile)
		globalStd.infoLogger = log.New(infoHandle, "INFO:  ", log.Ldate|log.Ltime|log.Lshortfile)
		globalStd.errorLogger = log.New(errorHandle, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
	} else {
		log.Println("Error: Global logger is not of type *defaultLogger, cannot set level.")
	}
}

// Debug logs a debug message using the global std logger.
func Debug(args ...interface{}) {
	std.Debug(args...)
}

// Debugf logs a debug message with formatting.
func Debugf(format string, args ...interface{}) {
	std.Debugf(format, args...)
}

// Info logs an informational message using the global std logger.
func Info(args ...interface{}) {
	std.Info(args...)
}

// Infof logs an informational message with formatting.
func Infof(format string, args ...interface{}) {
	std.Infof(format, args...)
}

// Error logs an error message.
func Error(args ...interface{}) {
	std.Error(args...)
}

// Errorf logs an error message with formatting.
func Errorf(format string, args ...interface{}) {
	std.Errorf(format, args...)
}

// Fatal logs a fatal error message and exits.
func Fatal(args ...interface{}) {
	std.Fatal(args...)
}

// Fatalf logs a fatal error message with formatting and exits.
func Fatalf(format string, args ...interface{}) {
	std.Fatalf(format, args...)
}
