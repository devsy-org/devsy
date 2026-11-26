package main

import (
	"fmt"
	"strings"
)

const maxStepSummaryDetailBytes = 32 << 10

var (
	workflowDataEscaper     = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")
	workflowPropertyEscaper = strings.NewReplacer(
		"%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C",
	)
)

// hangReporter leaves evidence of a setup hang in the step output (a GitHub
// warning annotation) and in the job step summary.
type hangReporter struct {
	log         *Logger
	files       FileSystem
	summaryPath string
}

func (r hangReporter) Report(title, message, details string) {
	r.log.Printf(
		"::warning title=%s::%s",
		workflowPropertyEscaper.Replace(title),
		workflowDataEscaper.Replace(message),
	)
	r.appendSummary(title, message, details)
}

func (r hangReporter) appendSummary(title, message, details string) {
	if r.summaryPath == "" || r.files == nil {
		return
	}
	out, err := r.files.OpenAppend(r.summaryPath)
	if err != nil {
		r.log.Printf("[podman-windows] step summary unavailable: %v", err)
		return
	}
	defer func() { _ = out.Close() }()
	_, _ = fmt.Fprintf(
		out,
		"### %s\n\n%s\n\n```\n%s\n```\n",
		title,
		message,
		tailBytes(details, maxStepSummaryDetailBytes),
	)
}

func tailBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[len(text)-limit:], "")
}
