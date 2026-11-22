// The harness shell: the smallest program that satisfies kagent's BYO contract.
//
// kagent's helper (app.New) owns everything kagent-specific: the A2A gRPC
// server on port 80, /readyz on 8081, the bootstrap fetch and the private
// task store. This file only decides what happens with one user message:
// it starts the loop program, hands it the message as one JSON line on stdin,
// and returns the loop's final JSON line as the agent's reply.
//
// Based on kagent's own fixture, go/core/test/byoa2a/main.go at v1.0.0-rc3.
package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"strings"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/kagent-dev/kagent/go/adk/pkg/app"
	"github.com/kagent-dev/kagent/go/adk/pkg/config"
	"github.com/kagent-dev/kagent/go/pkg/logging"
)

// loopCommand is started once per turn. Its stderr goes to the container log.
var loopCommand = []string{"node", "/app/loop.ts"}

// turnInput is the one line written to the loop's stdin.
type turnInput struct {
	ContextID string `json:"contextId"` // the kagent Session
	TaskID    string `json:"taskId"`    // this turn
	Text      string `json:"text"`      // the user's message
}

// loopEvent is one line read from the loop's stdout. Only "final" exists so far.
type loopEvent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type executor struct{}

// contextLog is where record writes. /data is the one folder that survives a turn.
const contextLog = "/data/executor-contexts.jsonl"

// record appends the value that Execute or Cancel was handed to contextLog, one
// JSON object on a line, so that it can be read back with `cat` in a later turn.
// It is a probe: it changes nothing about how a turn runs, and a failure to
// write is ignored.
func record(call string, request *a2asrv.ExecutorContext) {
	headers := map[string][]string{}
	if request.ServiceParams != nil {
		for name, values := range request.ServiceParams.List() {
			headers[name] = values
		}
	}
	line, err := json.Marshal(map[string]any{
		"call": call, "Message": request.Message, "TaskID": request.TaskID, "ContextID": request.ContextID,
		"StoredTask": request.StoredTask, "RelatedTasks": request.RelatedTasks, "Metadata": request.Metadata,
		"ServiceParams": headers, "User": request.User, "Tenant": request.Tenant,
	})
	if err != nil {
		return
	}
	file, err := os.OpenFile(contextLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	file.Write(append(line, '\n'))
}

// askPrefix marks a message that pauses the task with a question instead of
// running a command: "ask: Which version?" ends the turn in the state
// input-required, with "Which version?" as the question. It is a probe, there
// to see what a paused task looks like under kagent. The user's reply to the
// question is then run as a command like any other message.
const askPrefix = "ask:"

// Execute handles one user message. It yields "task submitted", then the loop's
// reply as an artifact and "completed"; or, if the loop failed, "failed" with
// the reason. A message that starts with askPrefix pauses the task instead.
func (executor) Execute(ctx context.Context, request *a2asrv.ExecutorContext) iter.Seq2[a2atype.Event, error] {
	return func(yield func(a2atype.Event, error) bool) {
		record("Execute", request)
		input := messageText(request.Message)
		// StoredTask is set when the user is replying to a paused task. That task
		// already exists, so it is not opened a second time.
		reply := request.StoredTask != nil
		if !reply {
			if !yield(a2atype.NewSubmittedTask(request, request.Message), nil) {
				return
			}
		}
		if question, ok := strings.CutPrefix(input, askPrefix); ok && !reply {
			message := a2atype.NewMessage(a2atype.MessageRoleAgent, a2atype.NewTextPart(strings.TrimSpace(question)))
			message.ContextID, message.TaskID = request.ContextID, request.TaskID
			yield(a2atype.NewStatusUpdateEvent(request, a2atype.TaskStateInputRequired, message), nil)
			return
		}
		text, err := runLoop(ctx, turnInput{
			ContextID: request.ContextID,
			TaskID:    string(request.TaskID),
			Text:      input,
		})
		if err != nil {
			message := a2atype.NewMessage(a2atype.MessageRoleAgent, a2atype.NewTextPart(err.Error()))
			message.ContextID, message.TaskID = request.ContextID, request.TaskID
			yield(a2atype.NewStatusUpdateEvent(request, a2atype.TaskStateFailed, message), nil)
			return
		}
		// The reply goes out as an artifact, which is how kagent's own harness sends
		// model text and what clients print. A message on the "completed" status is
		// not shown by a2a-chat.
		if !yield(a2atype.NewArtifactEvent(request, a2atype.NewTextPart(text)), nil) {
			return
		}
		yield(a2atype.NewStatusUpdateEvent(request, a2atype.TaskStateCompleted, nil), nil)
	}
}

// Cancel does nothing yet, as in kagent's fixture, except record what it was handed.
func (executor) Cancel(_ context.Context, request *a2asrv.ExecutorContext) iter.Seq2[a2atype.Event, error] {
	return func(func(a2atype.Event, error) bool) {
		record("Cancel", request)
	}
}

// runLoop runs the loop program for one turn and returns its final text.
func runLoop(ctx context.Context, input turnInput) (string, error) {
	line, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, loopCommand[0], loopCommand[1:]...)
	command.Stdin = bytes.NewReader(append(line, '\n'))
	command.Stderr = os.Stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := command.Start(); err != nil {
		return "", fmt.Errorf("start loop: %w", err)
	}

	var final *string
	lines := bufio.NewScanner(stdout)
	lines.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for lines.Scan() {
		var event loopEvent
		if json.Unmarshal(lines.Bytes(), &event) != nil || event.Type != "final" {
			// Not part of the protocol (a stray console.log, say): keep it as a log line.
			fmt.Fprintf(os.Stderr, "loop stdout: %s\n", lines.Bytes())
			continue
		}
		final = &event.Text
	}
	if err := errors.Join(lines.Err(), command.Wait()); err != nil {
		return "", fmt.Errorf("loop failed: %w", err)
	}
	if final == nil {
		return "", errors.New("loop exited without a final event")
	}
	return *final, nil
}

// messageText joins the text parts of an A2A message; other part kinds are ignored.
func messageText(message *a2atype.Message) string {
	var text strings.Builder
	if message != nil {
		for _, part := range message.Parts {
			text.WriteString(part.Text())
		}
	}
	return text.String()
}

func main() {
	// Fetch this revision's compiled config from the controller into /config.
	// The shell reads only its runtime section; the loop may read the rest.
	if err := config.MaterializeFromBootstrap(context.Background(), "/config"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	compiled, err := config.LoadAgentConfig("/config/config.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	runtime := config.ProcessConfig(compiled)
	logger, err := logging.New(os.Stderr, cmp.Or(runtime.LogLevel, "info"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	application, err := app.New(app.AppConfig{
		ControlPlaneURL:  runtime.ControlPlaneUrl,
		MaxContentLength: runtime.MaxContentLength,
		AgentCard: a2atype.AgentCard{
			Name:         "kagent-test-harness",
			Version:      "v1",
			Capabilities: a2atype.AgentCapabilities{Streaming: true},
		},
		Port:    fmt.Sprint(cmp.Or(runtime.GetPort(), 80)),
		AppName: cmp.Or(runtime.Name, "kagent-test-harness"),
		Logger:  logger,
	}, executor{})
	if err != nil {
		logger.Error("failed to create A2A application", "error", err)
		os.Exit(1)
	}
	if err := application.Run(); err != nil {
		logger.Error("A2A application stopped", "error", err)
		os.Exit(1)
	}
}
