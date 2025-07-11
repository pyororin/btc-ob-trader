// Package logger provides basic logging functionalities.
package logger

import (
	"io"
	"log"
	"os"
)

// Logger defines a simple interface for logging.
type Logger interface {
	Info(args ...interface{})
	Infof(format string, args ...interface{})
	Error(args ...interface{})
	Errorf(format string, args ...interface{})
	Fatal(args ...interface{})
	Fatalf(format string, args ...interface{})
}

// defaultLogger is a simple logger implementation using the standard log package.
type defaultLogger struct {
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

	// Basic log level handling: For now, "debug" won't show INFO, but this is very basic.
	// A more robust solution would involve proper log level constants and checks.
	// This is a placeholder for more advanced log level control.
	// Example: if logLevel is "error", infoLogger's output could be ioutil.Discard.
	// For now, we only differentiate by not setting infoLogger if level is too high.
	// This is not ideal, as it means Info calls would panic if not careful.
	// A better simple approach:
	var iLog, eLog, fLog *log.Logger

	iLog = log.New(infoHandle, "INFO:  ", log.Ldate|log.Ltime|log.Lshortfile)
	eLog = log.New(errorHandle, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
	fLog = log.New(fatalHandle, "FATAL: ", log.Ldate|log.Ltime|log.Lshortfile)

	// Crude log level filtering:
	// This example: "error" or "fatal" level will suppress "INFO" logs.
	// "fatal" will suppress "INFO" and "ERROR". (This is just an example, not fully robust)
	// A real implementation would use constants and a hierarchy.
	// For now, NewLogger doesn't control global state directly to avoid cycles.
	// It just returns a new logger configured to the given level.
	// The global `std` logger will be initialized separately.
	if logLevel == "error" || logLevel == "fatal" {
		iLog = log.New(io.Discard, "", 0) // Discard info logs for error/fatal level
	}
	if logLevel == "fatal" {
		eLog = log.New(io.Discard, "", 0) // Discard error logs for fatal level
	}

	return &defaultLogger{
		infoLogger:  iLog,
		errorLogger: eLog,
		fatalLogger: fLog,
	}
	// No longer updating global `std` here:
	// std = logger
	// return logger
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
	infoLogger:  log.New(os.Stdout, "INFO:  ", log.Ldate|log.Ltime|log.Lshortfile),
	errorLogger: log.New(os.Stderr, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile),
	fatalLogger: log.New(os.Stderr, "FATAL: ", log.Ldate|log.Ltime|log.Lshortfile),
}

// SetGlobalLogLevel reconfigures the global std logger's level.
// This is a basic implementation. A more robust one would parse levels carefully.
func SetGlobalLogLevel(logLevel string) {
	infoHandle := os.Stdout
	errorHandle := os.Stderr
	// fatalHandle := os.Stderr // fatal always goes to Stderr

	if logLevel == "error" || logLevel == "fatal" {
		infoHandle = io.Discard
	}
	if logLevel == "fatal" {
		errorHandle = io.Discard
	}

	// Update the global std logger's internal loggers
	// This requires std to be of a concrete type or have methods to set internal loggers.
	// For simplicity, let's assume std is *defaultLogger for this operation,
	// or we add methods to the Logger interface (which is more complex).
	// Casting to *defaultLogger for this internal function:
	if globalStd, ok := std.(*defaultLogger); ok {
		globalStd.infoLogger = log.New(infoHandle, "INFO:  ", log.Ldate|log.Ltime|log.Lshortfile)
		globalStd.errorLogger = log.New(errorHandle, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
		// fatalLogger usually isn't silenced by level, but if needed:
		// globalStd.fatalLogger = log.New(fatalHandle, "FATAL: ", log.Ldate|log.Ltime|log.Lshortfile)
	} else {
		// This case should ideally not happen if std is always *defaultLogger
		log.Println("Error: Global logger is not of type *defaultLogger, cannot set level.")
	}
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
