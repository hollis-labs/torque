package plugin

import (
	"fmt"
	"log"

	goplugin "github.com/hollis-labs/plugin"
)

type logger struct{ prefix string }

// NewLogger creates a new logger with the given prefix.
func NewLogger(prefix string) goplugin.Logger {
	return &logger{prefix: prefix}
}

func (l *logger) Debug(msg string, keysAndValues ...interface{}) {
	l.logf("DEBUG", msg, keysAndValues...)
}

func (l *logger) Info(msg string, keysAndValues ...interface{}) {
	l.logf("INFO", msg, keysAndValues...)
}

func (l *logger) Warn(msg string, keysAndValues ...interface{}) {
	l.logf("WARN", msg, keysAndValues...)
}

func (l *logger) Error(msg string, keysAndValues ...interface{}) {
	l.logf("ERROR", msg, keysAndValues...)
}

func (l *logger) With(keysAndValues ...interface{}) goplugin.Logger {
	extra := fmt.Sprintf(" %v", keysAndValues)
	return &logger{prefix: l.prefix + extra}
}

func (l *logger) logf(level, msg string, keysAndValues ...interface{}) {
	var kvStr string
	if len(keysAndValues) > 0 {
		kvStr = fmt.Sprintf(" %v", keysAndValues)
	}
	log.Printf("[%s] %s%s%s", level, l.prefix, msg, kvStr)
}
