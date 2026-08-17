package logger

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	ColorReset   = "\033[0m"
	ColorRed     = "\033[31m"
	ColorGreen   = "\033[32m"
	ColorYellow  = "\033[33m"
	ColorBlue    = "\033[34m"
	ColorMagenta = "\033[35m"
	ColorCyan    = "\033[36m"
	ColorGray    = "\033[90m"
	ColorBold    = "\033[1m"
)

type Logger struct {
	debug  bool
	silent bool
	mu     sync.Mutex
}

var l *Logger

func Init(debug bool) {
	l = &Logger{debug: debug}
}

func SetSilent(silent bool) {
	if l == nil {
		l = &Logger{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.silent = silent
}

func (l *Logger) Emit(level, color, reqID, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.silent {
		return
	}
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	rid := ""
	if reqID != "" {
		rid = fmt.Sprintf(" %s[%s]%s", ColorGray, reqID, ColorReset)
	}
	fmt.Printf("%s%s%s %s[%-5s]%s%s %s\n",
		ColorGray, ts, ColorReset,
		color+ColorBold, level, ColorReset,
		rid, msg,
	)
}

func Emit(level, color, reqID, format string, args ...any) {
	l.Emit(level, color, reqID, format, args...)
}

func Info(format string, args ...any)  { l.Emit("INFO", ColorGreen, "", format, args...) }
func Warn(format string, args ...any)  { l.Emit("WARN", ColorYellow, "", format, args...) }
func Error(format string, args ...any) { l.Emit("ERROR", ColorRed, "", format, args...) }
func Debug(format string, args ...any) {
	if l.debug {
		l.Emit("DEBUG", ColorCyan, "", format, args...)
	}
}
func ReqWarn(reqID, format string, args ...any) {
	l.Emit("WARN", ColorYellow, reqID, format, args...)
}
func ReqError(reqID, format string, args ...any) {
	l.Emit("ERROR", ColorRed, reqID, format, args...)
}
func ReqDebug(reqID, format string, args ...any) {
	if l.debug {
		l.Emit("DEBUG", ColorCyan, reqID, format, args...)
	}
}

func ModelStatus(reqID, status, model, keyHint string) {
	c, icon := ColorGreen, "✓"
	switch status {
	case "NG":
		c, icon = ColorYellow, "✗"
	case "COOLDOWN":
		c, icon = ColorRed, "⏸"
	case "DEFERRED":
		c, icon = ColorGray, "?"
	case "SLOW":
		c, icon = ColorYellow, "⚠"
	}
	keyStr := ""
	if keyHint != "" {
		keyStr = fmt.Sprintf("  %s(key:%s)%s", ColorMagenta, keyHint, ColorReset)
	}
	l.Emit("MODEL", c, reqID, "%s%s %s%s%s%s",
		c, icon, ColorBold, model, ColorReset, keyStr)
}

func Banner(title string, header []string, rows [][]string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.silent {
		return
	}

	allRows := rows
	if len(header) > 0 {
		allRows = append([][]string{header}, rows...)
	}

	cols := 0
	for _, r := range allRows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	widths := make([]int, cols)
	for _, r := range allRows {
		for c, cell := range r {
			if vl := visibleLen(cell); vl > widths[c] {
				widths[c] = vl
			}
		}
	}

	totalWidth := 4
	for _, w := range widths {
		totalWidth += w + 3
	}
	if totalWidth < 72 {
		totalWidth = 72
	}
	sep := strings.Repeat("─", totalWidth)

	fmt.Printf("\n%s%s%s\n", ColorCyan+ColorBold, title, ColorReset)
	fmt.Printf("%s%s%s\n", ColorGray, sep, ColorReset)
	for ri, r := range allRows {
		if ri == 1 && len(header) > 0 {
			fmt.Printf("%s%s%s\n", ColorGray, sep, ColorReset)
		}
		var sb strings.Builder
		sb.WriteString("  ")
		for c, cell := range r {
			pad := widths[c] - visibleLen(cell)
			sb.WriteString(cell)
			sb.WriteString(strings.Repeat(" ", pad))
			if c < len(r)-1 {
				sb.WriteString("   ")
			}
		}
		fmt.Println(sb.String())
	}
	fmt.Printf("%s%s%s\n\n", ColorGray, sep, ColorReset)
}

func visibleLen(s string) int {
	inEsc := false
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\033' {
			inEsc = true
		}
		if inEsc {
			if s[i] == 'm' {
				inEsc = false
			}
			continue
		}
		if s[i]&0xC0 != 0x80 {
			n++
		}
	}
	return n
}
