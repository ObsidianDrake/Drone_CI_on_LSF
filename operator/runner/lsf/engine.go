// Package lsf executes native tcsh jobs through the LSF command line tools.
package lsf

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/drone/drone-runtime/engine"
	"github.com/sirupsen/logrus"
)

// DebugRetainLabel retains the pipeline directory after normal lifecycle cleanup.
const DebugRetainLabel = "lsf.drone.io/debug-retain"

type Config struct {
	Bsub, Bjobs, Bkill                           string
	Workspace, Queue, Resources, Shell           string
	Slots                                        int
	PollInterval, CommandTimeout, CleanupTimeout time.Duration
	DebugRetention, DebugCleanupInterval         time.Duration
}

type Engine struct {
	config Config
	// TrackJob is configured before starting workers; repo zero marks completion.
	TrackJob  func(repo int64, id string, name string)
	mu        sync.Mutex
	pipelines map[*engine.Spec]*pipeline
}

type pipeline struct {
	mu               sync.Mutex // serializes submission against destruction
	ctx              context.Context
	cancel           context.CancelFunc
	dir, workspace   string
	jobs             map[*engine.Step]*job
	destroyed        bool
	submissionErr    error
	jobInfoDisabled  bool
	debugRetain      bool
	repoID           int64
	buildID, stageID int64
}

type job struct {
	options               []string
	id, dir, log, wrapper string
	done                  chan struct{}
	state                 engine.State // published by closing done
	err                   error
	confirmed             bool // scheduler confirmed the job is terminal
	stoppedByRunner       bool
	finished              time.Time
}

var _ engine.Engine = (*Engine)(nil)
var jobIDPattern = regexp.MustCompile(`Job\s+<([1-9][0-9]*)>`)
var envPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func New(config Config) (*Engine, error) {
	if config.Bsub == "" {
		config.Bsub = "bsub"
	}
	if config.Bjobs == "" {
		config.Bjobs = "bjobs"
	}
	if config.Bkill == "" {
		config.Bkill = "bkill"
	}
	if config.Shell == "" {
		config.Shell = "/bin/tcsh"
	}
	if _, err := shellArgs(config.Shell); err != nil {
		return nil, err
	}
	if config.Workspace == "" {
		config.Workspace = filepath.Join(os.TempDir(), "drone-lsf")
	}
	if config.Slots == 0 {
		config.Slots = 1
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Second
	}
	if config.CommandTimeout == 0 {
		config.CommandTimeout = 30 * time.Second
	}
	if config.CleanupTimeout == 0 {
		config.CleanupTimeout = time.Minute
	}
	if config.DebugRetention == 0 {
		config.DebugRetention = 7 * 24 * time.Hour
	}
	if config.DebugCleanupInterval == 0 {
		config.DebugCleanupInterval = time.Hour
	}
	if config.DebugRetention < 0 || config.DebugCleanupInterval < 0 {
		return nil, fmt.Errorf("lsf: Debug retention and cleanup interval must be positive")
	}
	if config.Slots < 1 || config.PollInterval < 0 || config.CommandTimeout < 0 || config.CleanupTimeout < 0 {
		return nil, fmt.Errorf("lsf: slots and timeouts must be positive")
	}
	for _, command := range []string{config.Bsub, config.Bjobs, config.Bkill} {
		if _, err := exec.LookPath(command); err != nil {
			return nil, fmt.Errorf("lsf: %w", err)
		}
	}
	var err error
	config.Workspace, err = filepath.Abs(config.Workspace)
	if err != nil {
		return nil, err
	}
	return &Engine{config: config, pipelines: make(map[*engine.Spec]*pipeline)}, nil
}

func (e *Engine) Setup(ctx context.Context, spec *engine.Spec) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(e.config.Workspace, 0700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(e.config.Workspace, "pipeline-")
	if err != nil {
		return err
	}
	work := filepath.Join(dir, "workspace")
	if err := os.Mkdir(work, 0700); err != nil {
		os.RemoveAll(dir)
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	p := &pipeline{debugRetain: spec.Metadata.Labels[DebugRetainLabel] == "true", jobInfoDisabled: spec.Metadata.Labels[JobInfoDisabledLabel] == "true", ctx: ctx, cancel: cancel, dir: dir, workspace: work, jobs: make(map[*engine.Step]*job)}
	p.repoID, _ = strconv.ParseInt(spec.Metadata.Labels["lsf.drone.io/repo-id"], 10, 64)
	p.buildID, _ = strconv.ParseInt(spec.Metadata.Labels["lsf.drone.io/build-id"], 10, 64)
	p.stageID, _ = strconv.ParseInt(spec.Metadata.Labels["lsf.drone.io/stage-id"], 10, 64)
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.pipelines[spec]; exists {
		cancel()
		os.RemoveAll(dir)
		return fmt.Errorf("lsf: pipeline already exists")
	}
	e.pipelines[spec] = p
	return nil
}

func (e *Engine) lookup(spec *engine.Spec) (*pipeline, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.pipelines[spec]
	if p == nil {
		return nil, fmt.Errorf("lsf: pipeline is not initialized")
	}
	return p, nil
}

func (e *Engine) Create(ctx context.Context, spec *engine.Spec, step *engine.Step) error {
	p, err := e.lookup(spec)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.destroyed {
		return fmt.Errorf("lsf: pipeline destroyed")
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, exists := p.jobs[step]; exists {
		return fmt.Errorf("lsf: step already exists")
	}
	if !relativePath(step.WorkingDir) {
		return fmt.Errorf("lsf: invalid working directory")
	}
	var script []byte
	nativeClone := false
	for _, file := range spec.Files {
		if file.Metadata.Name == step.Metadata.Name {
			script = file.Data
			nativeClone = file.Metadata.Labels["lsf.drone.io/clone"] == "true"
			break
		}
	}
	if len(script) == 0 {
		return fmt.Errorf("lsf: missing script for step %q", step.Metadata.Name)
	}
	dir, err := os.MkdirTemp(p.dir, "step-")
	if err != nil {
		return err
	}
	j := &job{dir: dir, log: filepath.Join(dir, "output.log"), wrapper: filepath.Join(dir, "run.sh"), done: make(chan struct{})}
	if err := os.WriteFile(filepath.Join(dir, "commands.script"), script, 0600); err != nil {
		return err
	}
	var initialLog []byte
	if p.debugRetain {
		initialLog = []byte(fmt.Sprintf("\n[Debug] Pipeline directory retained after completion: %s (retention: %s; expiry recorded in %s after confirmed completion)\n[Debug] LSF output: %s\n[Debug] LSF error: %s\n\n", p.dir, e.config.DebugRetention, debugRetentionFile, filepath.Join(j.dir, "scheduler.out"), filepath.Join(j.dir, "scheduler.err")))
	}
	if err := os.WriteFile(j.log, initialLog, 0600); err != nil {
		return err
	}
	// Overlay pipeline settings on the environment supplied by LSF at job
	// execution time. Do not replace the execution host's PATH or HOME.
	env := make(map[string]string)
	for key, value := range step.Envs {
		env[key] = value
	}
	for _, ref := range step.Secrets {
		found := false
		for _, secret := range spec.Secrets {
			if secret.Metadata.Name == ref.Name {
				env[ref.Env] = secret.Data
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("lsf: missing secret %q", ref.Name)
		}
	}
	j.options, err = splitOptions(env["BSUB_OPTION"])
	if err != nil {
		return err
	}
	shell := env["SHELL_TYPE"]
	if shell == "" {
		shell = e.config.Shell
	}
	// The generated clone script handles expected fetch failures itself and
	// uses POSIX syntax. SHELL_TYPE continues to control user command steps.
	if nativeClone {
		shell = "/bin/sh"
	}
	command, err := shellArgs(shell)
	if err != nil {
		return err
	}
	for _, prefix := range []string{"DRONE", "CI"} {
		env[prefix+"_WORKSPACE"] = p.workspace
		env[prefix+"_WORKSPACE_BASE"] = p.workspace
		env[prefix+"_WORKSPACE_PATH"] = ""
	}
	// Only native clone Git processes need the CI netrc. Ordinary commands
	// retain the account HOME, including its Git and LSF configuration.
	delete(env, "DRONE_LSF_CLONE_NETRC_HOME")
	if nativeClone && env["CI_NETRC_USERNAME"] != "" && env["CI_NETRC_PASSWORD"] != "" {
		home := filepath.Join(dir, "home")
		if err := os.Mkdir(home, 0700); err != nil {
			return err
		}
		// Keep ordinary tokens unquoted for older Git/libcurl netrc readers.
		// Quote special characters to prevent additional entries.
		netrc := fmt.Sprintf("machine %s login %s password %s\n", netrcToken(env["CI_NETRC_MACHINE"]), netrcToken(env["CI_NETRC_USERNAME"]), netrcToken(env["CI_NETRC_PASSWORD"]))
		if err := os.WriteFile(filepath.Join(home, ".netrc"), []byte(netrc), 0600); err != nil {
			return err
		}
		env["DRONE_LSF_CLONE_NETRC_HOME"] = home
	}
	for _, prefix := range []string{"CI", "DRONE"} {
		delete(env, prefix+"_NETRC_USERNAME")
		delete(env, prefix+"_NETRC_PASSWORD")
	}
	keys := make([]string, 0, len(env))
	for key, value := range env {
		if !envPattern.MatchString(key) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("lsf: invalid environment variable %q", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var wrapper strings.Builder
	// The tail reader may already have consumed the Debug header before LSF starts.
	// Never truncate this file or invalidate its current read offset.
	wrapper.WriteString("#!/bin/sh\nexec >>" + quote(j.log) + " 2>&1\ncd " + quote(filepath.Join(p.workspace, step.WorkingDir)) + " || exit 125\n")
	// A dedicated reader colors stderr as it arrives. Keep the command exit
	// status and drain the reader before reporting completion to LSF.
	stderrPipe := quote(filepath.Join(dir, "stderr.pipe"))
	wrapper.WriteString("/usr/bin/mkfifo " + stderrPipe + " || exit 125\n")
	wrapper.WriteString("(" + stderrReader(nativeClone) + ") < " + stderrPipe + " &\n")
	wrapper.WriteString("stderr_reader=$!\n")
	// This is runner-owned state, never inherited from a parent build.
	wrapper.WriteString("unset DRONE_LSF_CLONE_NETRC_HOME\n")
	wrapper.WriteString("/usr/bin/env")
	for _, key := range keys {
		wrapper.WriteString(" " + quote(key+"="+env[key]))
	}
	for _, key := range []string{"LSB_JOBID", "LSB_JOBNAME", "LSB_QUEUE", "LSB_DJOB_NUMPROC"} {
		wrapper.WriteString(" " + key + "=\"${" + key + ":-}\"")
	}
	for _, arg := range command {
		wrapper.WriteString(" " + quote(arg))
	}
	wrapper.WriteString(" " + quote(filepath.Join(dir, "commands.script")) + " 2>" + stderrPipe + "\n")
	wrapper.WriteString("command_status=$?\nwait \"$stderr_reader\"\nexit \"$command_status\"\n")
	if err := os.WriteFile(j.wrapper, []byte(wrapper.String()), 0600); err != nil {
		return err
	}
	p.jobs[step] = j
	return nil
}

func netrcToken(value string) string {
	if value == "" || strings.ContainsAny(value, " \t\r\n\v\f\\\"#") {
		return strconv.Quote(value)
	}
	return value
}

func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func (e *Engine) command(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, e.config.CommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("lsf: %s: %w: %s", filepath.Base(name), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func (e *Engine) Start(ctx context.Context, spec *engine.Spec, step *engine.Step) error {
	p, err := e.lookup(spec)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.destroyed {
		return fmt.Errorf("lsf: pipeline destroyed")
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	j := p.jobs[step]
	if j == nil {
		return fmt.Errorf("lsf: step is not created")
	}
	if j.id != "" {
		return fmt.Errorf("lsf: step already submitted")
	}
	name := stepJobName(spec, step)
	args := []string{"-J", name, "-cwd", p.workspace,
		"-oo", filepath.Join(j.dir, "scheduler.out"), "-eo", filepath.Join(j.dir, "scheduler.err"), "-env", "all"}
	has := func(option string) bool {
		for _, arg := range j.options {
			if arg == option || strings.HasPrefix(arg, option+"=") {
				return true
			}
		}
		return false
	}
	if !has("-n") {
		args = append(args, "-n", strconv.Itoa(e.config.Slots))
	}
	if e.config.Queue != "" && !has("-q") {
		args = append(args, "-q", e.config.Queue)
	}
	if e.config.Resources != "" && !has("-R") {
		args = append(args, "-R", e.config.Resources)
	}
	args = append(args, j.options...)
	args = append(args, "/bin/sh", j.wrapper)
	// Do not interrupt bsub on pipeline cancellation before reading its job ID.
	// The independent command timeout still bounds a stuck LSF client.
	output, submitErr := e.command(context.Background(), e.config.Bsub, args...)
	match := jobIDPattern.FindSubmatch(output)
	if len(match) != 2 {
		if submitErr != nil {
			p.submissionErr = fmt.Errorf("%w; submission outcome unknown; retained %s for reconciliation", submitErr, p.dir)
			logrus.Error(p.submissionErr)
			return submitErr
		}
		p.submissionErr = fmt.Errorf("lsf: bsub did not return a job ID: %s; retained %s for reconciliation", strings.TrimSpace(string(output)), p.dir)
		logrus.Error(p.submissionErr)
		return p.submissionErr
	}
	j.id = string(match[1])
	if e.TrackJob != nil && p.repoID > 0 {
		e.TrackJob(p.repoID, j.id, name)
	}
	logrus.WithFields(logrus.Fields{"job": j.id, "job_name": name, "step": step.Metadata.Name, "workspace": p.workspace}).Info("lsf: submitted job")

	go e.monitor(p, j)
	if err := os.WriteFile(filepath.Join(j.dir, "job.id"), []byte(j.id+"\n"), 0600); err != nil {
		return err
	}
	return submitErr
}

func (e *Engine) status(ctx context.Context, id string) (engine.State, bool, error) {
	output, err := e.command(ctx, e.config.Bjobs, "-a", "-noheader", "-o", "stat exit_code", id)
	if err != nil {
		return engine.State{}, false, err
	}
	fields := strings.Fields(string(output))
	if len(fields) < 1 {
		return engine.State{}, false, fmt.Errorf("lsf: empty bjobs response for %s", id)
	}
	switch fields[0] {
	case "DONE":
		return engine.State{Exited: true}, true, nil
	case "EXIT":
		code := 1
		if len(fields) > 1 {
			if n, err := strconv.Atoi(fields[1]); err == nil && n > 0 {
				code = n
			}
		}
		return engine.State{Exited: true, ExitCode: code}, true, nil
	case "PEND", "RUN", "WAIT", "PSUSP", "USUSP", "SSUSP", "PROV":
		return engine.State{}, false, nil
	default:
		return engine.State{}, false, fmt.Errorf("lsf: unexpected bjobs response for %s: %s", id, strings.TrimSpace(string(output)))
	}
}

func (e *Engine) monitor(p *pipeline, j *job) {
	defer func() {
		j.finished = time.Now()
		close(j.done)
	}()
	defer func() {
		if j.confirmed && e.TrackJob != nil && p.repoID > 0 {
			e.TrackJob(0, j.id, "")
		}
	}()
	defer func() {
		if j.confirmed && !p.jobInfoDisabled {
			e.appendJobSummary(j)
		}
	}()
	failures := 0
	for {
		if p.ctx.Err() != nil {
			j.err = p.ctx.Err()
			break
		}
		state, done, err := e.status(p.ctx, j.id)
		if done {
			j.state = state
			j.confirmed = true
			return
		}
		if err != nil {
			failures++
			if failures >= 3 {
				j.err = err
				break
			}
		} else {
			failures = 0
		}
		select {
		case <-p.ctx.Done():
		case <-time.After(e.config.PollInterval):
		}
	}
	// bkill only acknowledges a request. Confirm termination before removing files.
	ctx, cancel := context.WithTimeout(context.Background(), e.config.CleanupTimeout)
	defer cancel()
	var killErr error
	for ctx.Err() == nil {
		// A service may have exited just before shutdown. Observe its result
		// before requesting a kill so a natural failure is not called cleanup.
		if state, done, _ := e.status(ctx, j.id); done {
			j.state, j.confirmed = state, true
			return
		}
		if p.ctx.Err() != nil {
			j.stoppedByRunner = true
		}
		_, killErr = e.command(ctx, e.config.Bkill, j.id)
		state, done, _ := e.status(ctx, j.id)
		if done {
			j.state = state
			j.confirmed = true
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(e.config.PollInterval):
		}
	}
	j.state = engine.State{Exited: true, ExitCode: 137}
	j.err = fmt.Errorf("%v; lsf: cannot confirm termination of job %s (last bkill error: %v); retained %s", j.err, j.id, killErr, p.dir)
	logrus.Error(j.err)
}

func (e *Engine) findJob(spec *engine.Spec, step *engine.Step) (*job, error) {
	p, err := e.lookup(spec)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	j := p.jobs[step]
	if j == nil || j.id == "" {
		return nil, fmt.Errorf("lsf: step has not been submitted")
	}
	return j, nil
}

func (e *Engine) Wait(ctx context.Context, spec *engine.Spec, step *engine.Step) (*engine.State, error) {
	j, err := e.findJob(spec, step)
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-j.done:
		return &j.state, j.err
	}
}

func (e *Engine) Tail(ctx context.Context, spec *engine.Spec, step *engine.Step) (io.ReadCloser, error) {
	j, err := e.findJob(spec, step)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(j.log)
	if err != nil {
		return nil, err
	}
	reader, writer := io.Pipe()
	go func() {
		defer file.Close()
		defer writer.Close()
		buf := make([]byte, 32*1024)
		finished := false
		for {
			n, err := file.Read(buf)
			if n > 0 {
				// The legacy runtime creates an extra empty record when a write
				// contains multiple newline-terminated lines. Pipe writes stay
				// separate for its io.Copy reader: send one line fragment at a time.
				for _, part := range bytes.SplitAfter(buf[:n], []byte{'\n'}) {
					if len(part) == 0 {
						continue
					}
					if _, err := writer.Write(part); err != nil {
						return
					}
				}
			}
			if err != nil && err != io.EOF {
				writer.CloseWithError(err)
				return
			}
			if n > 0 {
				continue
			}
			if finished {
				return
			}
			select {
			case <-j.done:
				finished = true // one final read drains the completed log
			case <-ctx.Done():
				return
			case <-time.After(e.config.PollInterval):
			}
		}
	}()
	return reader, nil
}

func (e *Engine) Destroy(ctx context.Context, spec *engine.Spec) error {
	p, err := e.lookup(spec)
	if err != nil {
		return nil
	} // Setup can fail before registration.
	p.cancel()
	p.mu.Lock()
	p.destroyed = true
	var jobs []*job
	for _, j := range p.jobs {
		if j.id != "" {
			jobs = append(jobs, j)
		}
	}
	p.mu.Unlock()
	cleanupErr := p.submissionErr
	for _, j := range jobs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-j.done:
		}
		if !j.confirmed {
			cleanupErr = fmt.Errorf("lsf: job %s termination unconfirmed; retained %s: %v", j.id, p.dir, j.err)
		}
	}
	defer func() {
		e.mu.Lock()
		delete(e.pipelines, spec)
		e.mu.Unlock()
	}()
	if cleanupErr != nil {
		return cleanupErr
	}
	if p.debugRetain {
		return e.retainDebug(p, time.Now().UTC())
	}
	return os.RemoveAll(p.dir)
}

// Only known informational Git messages are white. Unknown diagnostics, warnings
// and errors remain red; user commands retain normal stderr coloring.
func stderrReader(nativeClone bool) string {
	var script strings.Builder
	script.WriteString("while IFS= read -r line || [ -n \"$line\" ]; do\n")
	if nativeClone {
		script.WriteString(`case "$line" in
  hint:*|"From "*|" * branch "*|" * [new branch] "*|" * [new tag] "*|"HEAD is now at "*|"remote: Enumerating objects:"*|"remote: Counting objects:"*|"remote: Compressing objects:"*|"remote: Total "*|"Receiving objects:"*|"Resolving deltas:"*|"Updating files:"*)
    printf '\033[0;37m%s\033[0;37m\n' "$line"
    continue ;;
esac
`)
	}
	script.WriteString("printf '\\033[31m%s\\033[0;37m\\n' \"$line\"\ndone")
	return script.String()
}
