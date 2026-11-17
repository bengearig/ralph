package loop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/headless"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/verify"
)

const (
	runsDirName   = "runs"
	dirMode       = 0o755
	fileMode      = 0o644
	nameTimeForm  = "20060102-150405"
	recordVersion = 1
	noVerifyText  = "no verification commands configured"
)

// RecorderOptions configure NewRecorder.
type RecorderOptions struct {
	Root string
	// Secrets are values to replace with [redacted]; those under 8 characters are ignored.
	Secrets []string
	// Now is the clock when nil.
	Now func() time.Time
}

type recorder struct {
	root string
	red  redactor
	now  func() time.Time
}

// NewRecorder keeps run records under the project's .ralph/runs directory.
func NewRecorder(opts RecorderOptions) Recorder {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &recorder{root: opts.Root, red: newRedactor(opts.Secrets), now: now}
}

func (r *recorder) Begin(_ context.Context, info AttemptInfo) (AttemptRecord, error) {
	if err := checkPart("slice", info.Slice); err != nil {
		return nil, err
	}
	if err := checkPart("task", info.Task); err != nil {
		return nil, err
	}
	parent := filepath.Join(project.RalphDir(r.root), runsDirName, info.Slice)
	if err := os.MkdirAll(parent, dirMode); err != nil {
		return nil, fmt.Errorf("create the run directory: %w", err)
	}
	base := fmt.Sprintf("%s-%03d-%s", r.now().UTC().Format(nameTimeForm), info.Attempt, info.Task)
	dir, err := makeUnique(parent, base)
	if err != nil {
		return nil, fmt.Errorf("create the run directory: %w", err)
	}
	out, err := os.OpenFile(filepath.Join(dir, "output.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileMode)
	if err != nil {
		return nil, fmt.Errorf("create output.jsonl: %w", err)
	}
	return &record{r: r, info: info, dir: dir, outFile: out, out: newLineWriter(r.red, out)}, nil
}

// checkPart refuses a name that could lead out of the runs directory.
func checkPart(what, s string) error {
	if s == "" || strings.ContainsAny(s, `/\`) || strings.Contains(s, "..") || strings.ContainsRune(s, 0) {
		return fmt.Errorf("the %s %q cannot name a run record directory", what, s)
	}
	return nil
}

func makeUnique(parent, base string) (string, error) {
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		dir := filepath.Join(parent, name)
		err := os.Mkdir(dir, dirMode)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
}

type record struct {
	r       *recorder
	info    AttemptInfo
	dir     string
	outFile *os.File
	out     *lineWriter
}

func (rc *record) Stdout() io.Writer { return rc.out }

func (rc *record) Prompt(text string) error {
	return rc.write("prompt.md", []byte(rc.r.red.String(text)))
}

func (rc *record) Verification(report verify.Report) error {
	return rc.write("verify.txt", []byte(rc.r.red.String(verifyText(report))))
}

func (rc *record) write(name string, data []byte) error {
	if err := os.WriteFile(filepath.Join(rc.dir, name), data, fileMode); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

func verifyText(report verify.Report) string {
	if len(report.Results) == 0 {
		return noVerifyText + "\n"
	}
	blocks := make([]string, len(report.Results))
	for i, res := range report.Results {
		how := fmt.Sprintf("exit %d", res.ExitCode)
		if res.TimedOut {
			how = "timed out"
		}
		out := strings.TrimRight(res.Output, "\n")
		if res.StartErr != "" {
			out = strings.TrimLeft(strings.Join([]string{"could not start: " + res.StartErr, out}, "\n"), "\n")
		}
		block := fmt.Sprintf("$ %s\n%s\n%s\n", res.Command, how, res.Duration.Round(time.Millisecond))
		if out != "" {
			block += "\n" + out + "\n"
		}
		blocks[i] = block
	}
	return strings.Join(blocks, "\n")
}

type recordDenial struct {
	Tool       string `json:"tool"`
	Target     string `json:"target"`
	Reason     string `json:"reason"`
	Suggestion string `json:"suggestion"`
}

type recordUsage struct {
	CostUSD          float64 `json:"cost_usd"`
	CostReported     bool    `json:"cost_reported"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	ReasoningTokens  int     `json:"reasoning_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
}

type recordResult struct {
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
}

type recordVerification struct {
	Passed  bool           `json:"passed"`
	Results []recordResult `json:"results"`
}

type recordDocument struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Included bool   `json:"included"`
	Reason   string `json:"reason"`
}

type recordFile struct {
	Version      int                `json:"version"`
	Slice        string             `json:"slice"`
	Task         string             `json:"task"`
	Attempt      int                `json:"attempt"`
	StartedAt    string             `json:"started_at"`
	DurationMS   int64              `json:"duration_ms"`
	Harness      string             `json:"harness"`
	Model        string             `json:"model"`
	Mode         string             `json:"mode"`
	StartHead    string             `json:"start_head"`
	EndHead      string             `json:"end_head"`
	Outcome      string             `json:"outcome"`
	Failure      string             `json:"failure"`
	Commit       string             `json:"commit"`
	ExitCode     int                `json:"exit_code"`
	TimedOut     bool               `json:"timed_out"`
	Denials      []recordDenial     `json:"denials"`
	Usage        recordUsage        `json:"usage"`
	Notes        []string           `json:"notes"`
	Verification recordVerification `json:"verification"`
	Documents    []recordDocument   `json:"documents"`
}

func (rc *record) Finish(res AttemptResult, run headless.Result) (string, error) {
	errs := []error{}
	if err := rc.out.Close(); err != nil {
		errs = append(errs, fmt.Errorf("write output.jsonl: %w", err))
	}
	if err := rc.outFile.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close output.jsonl: %w", err))
	}
	if err := rc.writeJSON(rc.build(res, run)); err != nil {
		errs = append(errs, err)
	}
	rel, err := filepath.Rel(rc.r.root, rc.dir)
	if err != nil {
		rel = rc.dir
	}
	return filepath.ToSlash(rel), errors.Join(errs...)
}

func (rc *record) build(res AttemptResult, run headless.Result) recordFile {
	red, info, u := rc.r.red, rc.info, run.Outcome.Usage
	f := recordFile{
		Version:    recordVersion,
		Slice:      info.Slice,
		Task:       info.Task,
		Attempt:    info.Attempt,
		StartedAt:  info.Started.UTC().Format(time.RFC3339),
		DurationMS: res.Duration.Milliseconds(),
		Harness:    info.Harness,
		Model:      info.Model,
		Mode:       string(info.Mode),
		StartHead:  info.StartHead,
		EndHead:    res.EndHead,
		Outcome:    string(res.Outcome),
		Failure:    red.String(res.Failure),
		Commit:     res.Commit,
		ExitCode:   run.ExitCode,
		TimedOut:   run.TimedOut,
		Denials:    []recordDenial{},
		Usage: recordUsage{
			CostUSD: u.CostUSD, CostReported: u.CostReported, InputTokens: u.InputTokens,
			OutputTokens: u.OutputTokens, ReasoningTokens: u.ReasoningTokens,
			CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens,
		},
		Notes:        red.Strings(run.Notes),
		Verification: recordVerification{Passed: res.Verification.Passed(), Results: []recordResult{}},
		Documents:    []recordDocument{},
	}
	for _, d := range run.Outcome.Denials {
		f.Denials = append(f.Denials, recordDenial{
			Tool: d.Tool, Target: red.String(d.Target), Reason: red.String(d.Reason),
			Suggestion: red.String(headless.Suggest(d)),
		})
	}
	for _, v := range res.Verification.Results {
		f.Verification.Results = append(f.Verification.Results, recordResult{
			Command: red.String(v.Command), ExitCode: v.ExitCode,
			DurationMS: v.Duration.Milliseconds(), TimedOut: v.TimedOut,
		})
	}
	for _, d := range res.Prompt.Documents {
		f.Documents = append(f.Documents, recordDocument{
			ID: d.ID, Title: red.String(d.Title), Included: d.Included, Reason: red.String(d.Reason),
		})
	}
	return f
}

// writeJSON writes record.json whole or not at all.
func (rc *record) writeJSON(f recordFile) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return fmt.Errorf("encode record.json: %w", err)
	}
	tmp, err := os.CreateTemp(rc.dir, ".record.json-*")
	if err != nil {
		return fmt.Errorf("write record.json: %w", err)
	}
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write record.json: %w", err)
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(fileMode); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write record.json: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(rc.dir, "record.json")); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write record.json: %w", err)
	}
	return nil
}

// AttemptSummary is the note text for an attempt: how it ended, where its
// record is and what to allow after a denial.
func AttemptSummary(attempt, max int, res AttemptResult, secrets []string) string {
	tail := res.Duration.Round(time.Second).String()
	if u := res.Run.Outcome.Usage; u.CostReported {
		tail += fmt.Sprintf(", $%.2f", u.CostUSD)
	}
	var head string
	if res.Outcome == OutcomeDone {
		head = fmt.Sprintf("Attempt %d of %d passed: commit %s (%s)", attempt, max, short(res.Commit), tail)
	} else {
		head = fmt.Sprintf("Attempt %d of %d failed: %s (%s)", attempt, max, res.Failure, tail)
	}
	lines := []string{head}
	if res.RecordPath != "" {
		lines = append(lines, "Run record: "+res.RecordPath)
	}
	lines = append(lines, res.Run.Suggestions()...)
	for _, e := range res.RecordErrors {
		lines = append(lines, "Run record could not be written: "+e)
	}
	return newRedactor(secrets).String(strings.Join(lines, "\n"))
}
