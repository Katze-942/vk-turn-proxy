package tunnel

// LogWriter is the interface that callers must implement to receive log messages.
type LogWriter interface {
	WriteLog(message string)
}

// defaultLogger writes to nowhere (discards logs).
type defaultLogger struct{}

func (d *defaultLogger) WriteLog(string) {}
