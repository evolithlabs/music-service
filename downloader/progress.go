package downloader

import (
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
)

type downloadProgress struct {
	Percent *float64
	Stage   string
}

// Progress is transient; the durable job status still recovers interrupted jobs.
var activeProgress sync.Map

type progressOutput struct {
	jobID   string
	output  io.Writer
	pending string
}

func (p *progressOutput) Write(data []byte) (int, error) {
	n, err := p.output.Write(data)
	p.pending += string(data[:n])
	for {
		end := strings.IndexAny(p.pending, "\r\n")
		if end < 0 {
			break
		}
		updateProgress(p.jobID, p.pending[:end])
		p.pending = p.pending[end+1:]
	}
	if len(p.pending) > 1<<20 {
		p.pending = ""
	}
	return n, err
}

func updateProgress(id, line string) {
	line = strings.TrimSpace(line)
	if line == "GROOVIO_PROCESSING" {
		percent := float64(100)
		activeProgress.Store(id, downloadProgress{Percent: &percent, Stage: "processing"})
		return
	}
	const prefix = "GROOVIO_PROGRESS:"
	if !strings.HasPrefix(line, prefix) {
		return
	}
	value := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, prefix)), "%"))
	percent, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(percent) || math.IsInf(percent, 0) {
		return
	}
	percent = math.Max(0, math.Min(100, percent))
	activeProgress.Store(id, downloadProgress{Percent: &percent, Stage: "downloading"})
}
