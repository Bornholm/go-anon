package modelstore

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

type ProgressBar struct {
	mu       sync.Mutex
	total    int64
	done     int64
	lastLine int
	lang     string
	callback func(lang string, done, total int64)
	isTTY    bool
}

func NewProgressBar(lang string, total int64, callback func(string, int64, int64), isTTY bool) *ProgressBar {
	return &ProgressBar{
		lang:     lang,
		total:    total,
		callback: callback,
		isTTY:    isTTY,
	}
}

func (pb *ProgressBar) Write(p []byte) (int, error) {
	n := len(p)

	pb.mu.Lock()
	pb.done += int64(n)
	done := pb.done
	total := pb.total
	pb.mu.Unlock()

	if pb.callback != nil {
		pb.callback(pb.lang, done, total)
	}

	if pb.isTTY {
		pb.render(done, total)
	}

	return n, nil
}

func (pb *ProgressBar) render(done, total int64) {
	pct := float64(done) / float64(total) * 100
	filled := int(pct / 5)
	if filled > 20 {
		filled = 20
	}
	bar := strings.Repeat("\u2588", filled) + strings.Repeat("\u2591", 20-filled)
	line := fmt.Sprintf("\r[%s] %5.1f%% %s/%s %s",
		bar,
		pct,
		formatBytes(done),
		formatBytes(total),
		pb.lang,
	)

	pb.clearPrevious()
	fmt.Fprint(os.Stderr, line)
	pb.lastLine = len(line)
}

func (pb *ProgressBar) clearPrevious() {
	if pb.lastLine > 0 {
		fmt.Fprintf(os.Stderr, "\r%s\r", strings.Repeat(" ", pb.lastLine))
	}
}

func (pb *ProgressBar) Finish() {
	pb.mu.Lock()
	total := pb.total
	pb.mu.Unlock()

	if pb.isTTY {
		pb.clearPrevious()
		line := fmt.Sprintf("\r[%s] 100.0%% %s/%s %s - done\n",
			strings.Repeat("\u2588", 20),
			formatBytes(total),
			formatBytes(total),
			pb.lang,
		)
		fmt.Fprint(os.Stderr, line)
	}
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB"}
	if exp >= len(units) {
		exp = len(units) - 1
	}
	return fmt.Sprintf("%.1f %s", float64(b)/float64(div), units[exp])
}

type progressWriter struct {
	pw *ProgressBar
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	return pw.pw.Write(p)
}
