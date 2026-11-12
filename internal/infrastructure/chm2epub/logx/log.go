// Package logx 提供极简的分级日志，批量转换时按文件前缀聚合输出。
package logx

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// Level 日志级别，数值越大越啰嗦。
type Level int

const (
	LevelError Level = iota
	LevelWarn
	LevelInfo
	LevelDebug
)

// Logger 是并发安全的简单日志器。
type Logger struct {
	mu     *sync.Mutex
	w      io.Writer
	level  Level
	prefix string
}

// New 创建日志器。
func New(w io.Writer, level Level) *Logger {
	if w == nil {
		w = os.Stdout
	}
	return &Logger{mu: &sync.Mutex{}, w: w, level: level}
}

// Default 返回写往标准输出的日志器。
func Default(level Level) *Logger { return New(os.Stdout, level) }

// WithPrefix 派生一个带固定前缀的日志器，共享同一把锁与输出。
func (l *Logger) WithPrefix(prefix string) *Logger {
	if l == nil {
		return nil
	}
	if prefix != "" {
		prefix += " "
	}
	return &Logger{mu: l.mu, w: l.w, level: l.level, prefix: prefix}
}

// SetLevel 调整级别。
func (l *Logger) SetLevel(level Level) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

func (l *Logger) logf(level Level, tag, format string, args ...any) {
	if l == nil || level > l.level {
		return
	}
	msg := fmt.Sprintf(format, args...)
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "%s%-5s %s\n", l.prefix, tag, msg)
}

func (l *Logger) Errorf(format string, args ...any) { l.logf(LevelError, "error", format, args...) }
func (l *Logger) Warnf(format string, args ...any)  { l.logf(LevelWarn, "warn", format, args...) }
func (l *Logger) Infof(format string, args ...any)  { l.logf(LevelInfo, "info", format, args...) }
func (l *Logger) Debugf(format string, args ...any) { l.logf(LevelDebug, "debug", format, args...) }
