package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

const testSummaryPath = "summary.md"

type summaryFS struct {
	FileSystem
	appended map[string]*summaryFile
	openErr  error
}

type summaryFile struct{ bytes.Buffer }

func (*summaryFile) Close() error { return nil }

func (f *summaryFS) OpenAppend(path string) (io.WriteCloser, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	if f.appended == nil {
		f.appended = map[string]*summaryFile{}
	}
	file := &summaryFile{}
	f.appended[path] = file
	return file, nil
}

func TestHangReporterWritesEscapedAnnotationAndSummary(t *testing.T) {
	var out bytes.Buffer
	files := &summaryFS{}
	reporter := hangReporter{
		log:         &Logger{Out: &out},
		files:       files,
		summaryPath: testSummaryPath,
	}

	reporter.Report("title: a,b", "100%\nsecond line", "diagnostic output")

	wantAnnotation := "::warning title=title%3A a%2Cb::100%25%0Asecond line\n"
	if out.String() != wantAnnotation {
		t.Fatalf("annotation = %q, want %q", out.String(), wantAnnotation)
	}
	summary := files.appended[testSummaryPath].String()
	for _, want := range []string{"### title: a,b", "100%\nsecond line", "diagnostic output"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %s", want, summary)
		}
	}
}

func TestHangReporterTruncatesSummaryDetailsKeepingTail(t *testing.T) {
	files := &summaryFS{}
	reporter := hangReporter{log: &Logger{Out: io.Discard}, files: files, summaryPath: "s.md"}
	details := "HEAD" + strings.Repeat("x", maxStepSummaryDetailBytes) + "TAIL"

	reporter.Report("title", "message", details)

	summary := files.appended["s.md"].String()
	if strings.Contains(summary, "HEAD") || !strings.Contains(summary, "TAIL") {
		t.Fatalf("summary did not keep the tail of the details: %.80s", summary)
	}
}

func TestHangReporterSummaryFailureDoesNotHideAnnotation(t *testing.T) {
	var out bytes.Buffer
	reporter := hangReporter{
		log:         &Logger{Out: &out},
		files:       &summaryFS{openErr: errors.New("read-only")},
		summaryPath: testSummaryPath,
	}

	reporter.Report("title", "message", "details")

	if !strings.Contains(out.String(), "::warning title=title::message") ||
		!strings.Contains(out.String(), "step summary unavailable: read-only") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestHangReporterWithoutSummaryPathOnlyAnnotates(t *testing.T) {
	files := &summaryFS{}
	var out bytes.Buffer
	hangReporter{log: &Logger{Out: &out}, files: files}.Report("title", "message", "details")

	if len(files.appended) != 0 || !strings.Contains(out.String(), "::warning") {
		t.Fatalf("appended=%v output=%q", files.appended, out.String())
	}
}
