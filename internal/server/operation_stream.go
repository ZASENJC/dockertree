package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"dockertree/internal/core"
	"dockertree/internal/docker"
)

const operationStreamMediaType = "application/x-ndjson"

type operationStreamContextKey struct{}

type operationStreamEvent struct {
	Type     string             `json:"type"`
	Data     string             `json:"data,omitempty"`
	Progress *operationProgress `json:"progress,omitempty"`
	Status   int                `json:"status,omitempty"`
	Result   json.RawMessage    `json:"result,omitempty"`
}

type operationProgress struct {
	Label  string `json:"label,omitempty"`
	ID     string `json:"id"`
	Status string `json:"status"`
	Text   string `json:"text,omitempty"`
}

type operationStream struct {
	mu      sync.Mutex
	encoder *json.Encoder
	flusher http.Flusher
}

func (s *operationStream) emit(event operationStreamEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.encoder.Encode(event); err == nil {
		s.flusher.Flush()
	}
}

func operationStreamFromContext(ctx context.Context) *operationStream {
	stream, _ := ctx.Value(operationStreamContextKey{}).(*operationStream)
	return stream
}

func (s *Server) streamOperationResponse(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		next(w, r)
		return
	}
	if r.Body != nil {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	w.Header().Set("Content-Type", operationStreamMediaType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	stream := &operationStream{encoder: json.NewEncoder(w), flusher: flusher}
	stream.emit(operationStreamEvent{Type: "start"})

	buffered := newBufferedResponseWriter()
	ctx := context.WithValue(r.Context(), operationStreamContextKey{}, stream)
	next(buffered, r.WithContext(ctx))

	status := buffered.status
	if status == 0 {
		status = http.StatusOK
	}
	body := bytes.TrimSpace(buffered.body.Bytes())
	if len(body) == 0 {
		body = []byte("null")
	}
	if !json.Valid(body) {
		body, _ = json.Marshal(map[string]string{"error": string(body)})
	}
	stream.emit(operationStreamEvent{Type: "result", Status: status, Result: json.RawMessage(body)})
}

func (s *Server) execute(ctx context.Context, cmd docker.Command) (docker.Result, error) {
	progressParser := operationProgressParserFor(cmd)
	stream := operationStreamFromContext(ctx)
	if stream == nil {
		result, err := s.exec.Execute(ctx, cmd)
		return compactOperationProgressResult(progressParser, result), err
	}
	stream.emit(operationStreamEvent{Type: "command", Data: cmd.RedactedString()})
	emitter := newOperationOutputEmitter(stream, progressParser)
	if streamingExec, ok := s.exec.(docker.StreamingExecutor); ok {
		result, err := streamingExec.ExecuteStream(ctx, cmd, func(chunk []byte) {
			emitter.emit(chunk)
		})
		emitter.flush()
		result = compactOperationProgressResult(progressParser, result)
		if result.Error != "" {
			stream.emit(operationStreamEvent{Type: "error", Data: result.Error})
		}
		return result, err
	}
	result, err := s.exec.Execute(ctx, cmd)
	if result.Output != "" {
		emitter.emit([]byte(result.Output))
	}
	emitter.flush()
	result = compactOperationProgressResult(progressParser, result)
	if result.Error != "" {
		stream.emit(operationStreamEvent{Type: "error", Data: result.Error})
	}
	return result, err
}

type operationProgressParser struct {
	label string
	parse func(string) (operationProgress, bool)
}

type operationOutputEmitter struct {
	stream  *operationStream
	parser  *operationProgressParser
	pending string
}

func newOperationOutputEmitter(stream *operationStream, parser *operationProgressParser) *operationOutputEmitter {
	return &operationOutputEmitter{stream: stream, parser: parser}
}

func (e *operationOutputEmitter) emit(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	if e.parser == nil {
		e.stream.emit(operationStreamEvent{Type: "output", Data: string(chunk)})
		return
	}
	e.pending += string(chunk)
	for {
		separator := operationProgressSeparator(e.pending)
		if separator == -1 {
			return
		}
		line := e.pending[:separator]
		e.pending = e.pending[separator+1:]
		e.emitLine(line, true)
	}
}

func (e *operationOutputEmitter) flush() {
	if e.parser == nil || e.pending == "" {
		return
	}
	e.emitLine(e.pending, false)
	e.pending = ""
}

func (e *operationOutputEmitter) emitLine(line string, newline bool) {
	if progress, ok := e.parser.parse(line); ok {
		progress.Label = e.parser.label
		e.stream.emit(operationStreamEvent{Type: "progress", Progress: &progress})
		return
	}
	if newline {
		line += "\n"
	}
	if line != "" {
		e.stream.emit(operationStreamEvent{Type: "output", Data: line})
	}
}

func operationProgressSeparator(value string) int {
	newline := strings.IndexByte(value, '\n')
	carriageReturn := strings.IndexByte(value, '\r')
	if newline == -1 {
		return carriageReturn
	}
	if carriageReturn == -1 || newline < carriageReturn {
		return newline
	}
	return carriageReturn
}

func operationProgressParserFor(cmd docker.Command) *operationProgressParser {
	if isComposeJSONProgressCommand(cmd) {
		label := "操作进度"
		if commandHasArg(cmd, "pull") {
			label = "镜像拉取"
		} else if commandHasArg(cmd, "up") {
			label = "部署进度"
		}
		return &operationProgressParser{label: label, parse: parseComposeProgressLine}
	}
	if cmd.Name == "docker" && len(cmd.Args) > 0 && (cmd.Args[0] == "run" || cmd.Args[0] == "pull") {
		label := "镜像拉取"
		if cmd.Args[0] == "run" {
			label = "部署进度"
		}
		return &operationProgressParser{label: label, parse: parseDockerPullProgressLine}
	}
	return nil
}

func isComposeJSONProgressCommand(cmd docker.Command) bool {
	if cmd.Name != "docker" || len(cmd.Args) == 0 || cmd.Args[0] != "compose" {
		return false
	}
	for i, arg := range cmd.Args {
		if arg == "--progress" && i+1 < len(cmd.Args) && cmd.Args[i+1] == "json" {
			return true
		}
	}
	return false
}

func commandHasArg(cmd docker.Command, target string) bool {
	for _, arg := range cmd.Args {
		if arg == target {
			return true
		}
	}
	return false
}

func parseComposeProgressLine(line string) (operationProgress, bool) {
	var progress operationProgress
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &progress); err != nil {
		return operationProgress{}, false
	}
	if progress.Status == "" || (progress.ID == "" && progress.Text == "") {
		return operationProgress{}, false
	}
	if progress.ID == "" {
		progress.ID = progress.Text
	}
	return progress, true
}

var dockerPullProgressPattern = regexp.MustCompile(`^([a-fA-F0-9]{6,64}):\s+(.+)$`)
var dockerPullBarPattern = regexp.MustCompile(`\s*\[[=>\-\s]+\]\s*`)

func parseDockerPullProgressLine(line string) (operationProgress, bool) {
	match := dockerPullProgressPattern.FindStringSubmatch(strings.TrimSpace(line))
	if len(match) != 3 {
		return operationProgress{}, false
	}
	text := strings.TrimSpace(dockerPullBarPattern.ReplaceAllString(match[2], " "))
	status := "Working"
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "pull complete"), strings.Contains(lower, "already exists"):
		status = "Done"
	case strings.Contains(lower, "error"), strings.Contains(lower, "failed"), strings.Contains(lower, "denied"):
		status = "Error"
	}
	return operationProgress{ID: match[1], Status: status, Text: text}, true
}

func compactOperationProgressResult(parser *operationProgressParser, result docker.Result) docker.Result {
	if parser == nil || result.Output == "" {
		return result
	}
	progressByID := map[string]operationProgress{}
	plainLines := make([]string, 0)
	for _, line := range strings.FieldsFunc(result.Output, func(char rune) bool { return char == '\n' || char == '\r' }) {
		if progress, ok := parser.parse(line); ok {
			progressByID[progress.ID] = progress
			continue
		}
		if strings.TrimSpace(line) != "" {
			plainLines = append(plainLines, strings.TrimSpace(line))
		}
	}
	if len(progressByID) == 0 {
		return result
	}
	done := 0
	for _, progress := range progressByID {
		if strings.EqualFold(progress.Status, "done") {
			done++
		}
	}
	progressLabel := parser.label
	if !strings.HasSuffix(progressLabel, "进度") {
		progressLabel += "进度"
	}
	summary := fmt.Sprintf("%s：%d/%d", progressLabel, done, len(progressByID))
	if done == len(progressByID) {
		summary = fmt.Sprintf("%s完成：%d/%d", strings.TrimSuffix(parser.label, "进度"), done, len(progressByID))
	}
	result.Output = strings.Join(append([]string{summary}, plainLines...), "\n")
	return result
}

func (s *Server) checkUpdate(ctx context.Context, project core.Project) (core.UpdateCheck, error) {
	stream := operationStreamFromContext(ctx)
	if stream == nil {
		return s.exec.CheckUpdate(ctx, project)
	}
	streamingChecker, ok := s.exec.(docker.StreamingUpdateChecker)
	if !ok {
		return s.exec.CheckUpdate(ctx, project)
	}
	check, err := streamingChecker.CheckUpdateStream(ctx, project, func(cmd docker.Command) {
		stream.emit(operationStreamEvent{Type: "command", Data: cmd.RedactedString()})
	}, func(chunk []byte) {
		stream.emit(operationStreamEvent{Type: "output", Data: string(chunk)})
	})
	if check.Error != "" {
		stream.emit(operationStreamEvent{Type: "error", Data: check.Error})
	}
	return check, err
}

type bufferedResponseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{header: make(http.Header)}
}

func (w *bufferedResponseWriter) Header() http.Header {
	return w.header
}

func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *bufferedResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

func acceptsOperationStream(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), operationStreamMediaType)
}
