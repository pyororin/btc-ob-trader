package csvwriter

import (
	"encoding/csv"
	"fmt"
	"os"
	"sync"

	"go.uber.org/zap"
)

// Writer is a simple CSV writer.
type Writer struct {
	file   *os.File
	writer *csv.Writer
	logger *zap.Logger
	mu     sync.Mutex
}

// NewWriter creates a new CSV writer.
func NewWriter(filePath string, logger *zap.Logger) (*Writer, error) {
	file, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create CSV file: %w", err)
	}

	writer := csv.NewWriter(file)

	return &Writer{
		file:   file,
		writer: writer,
		logger: logger,
	}, nil
}

// Write writes a record to the CSV file.
func (w *Writer) Write(record []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.writer.Write(record); err != nil {
		return fmt.Errorf("failed to write record to CSV: %w", err)
	}
	return nil
}

// Flush flushes any buffered data to the underlying file.
func (w *Writer) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writer.Flush()
}

// Close closes the file.
func (w *Writer) Close() error {
	w.Flush()
	return w.file.Close()
}
