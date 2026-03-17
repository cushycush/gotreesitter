package grammarlsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"

	"go.lsp.dev/jsonrpc2"
)

// Config configures the LSP proxy.
type Config struct {
	GoplsPath  string      // path to gopls binary (default: "gopls")
	ShadowDir  string      // directory for shadow .go files
	Extensions []Extension // registered grammar extensions
	Logger     *log.Logger
}

// Proxy sits between the editor and gopls, intercepting DSL files.
type Proxy struct {
	config   Config
	docs     *DocumentManager
	gopls    jsonrpc2.Conn
	client   jsonrpc2.Conn
	goplsCmd *exec.Cmd
	logger   *log.Logger
}

func NewProxy(config Config) *Proxy {
	if config.GoplsPath == "" {
		config.GoplsPath = "gopls"
	}
	if config.Logger == nil {
		config.Logger = log.New(os.Stderr, "[grammarlsp] ", log.LstdFlags)
	}
	return &Proxy{
		config: config,
		docs:   NewDocumentManager(config.ShadowDir, config.Extensions),
		logger: config.Logger,
	}
}

func (p *Proxy) Run(ctx context.Context, stdin io.ReadCloser, stdout io.WriteCloser) error {
	if err := p.startGopls(ctx); err != nil {
		return fmt.Errorf("start gopls: %w", err)
	}

	editorStream := jsonrpc2.NewStream(newReadWriteCloser(stdin, stdout))
	p.client = jsonrpc2.NewConn(editorStream)
	p.client.Go(ctx, p.handleEditorRequest)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.client.Done():
		return nil
	}
}

func (p *Proxy) startGopls(ctx context.Context) error {
	p.goplsCmd = exec.CommandContext(ctx, p.config.GoplsPath, "serve")
	goplsIn, err := p.goplsCmd.StdinPipe()
	if err != nil {
		return err
	}
	goplsOut, err := p.goplsCmd.StdoutPipe()
	if err != nil {
		return err
	}
	p.goplsCmd.Stderr = os.Stderr

	if err := p.goplsCmd.Start(); err != nil {
		return fmt.Errorf("exec gopls: %w", err)
	}

	goplsStream := jsonrpc2.NewStream(newReadWriteCloser(io.NopCloser(goplsOut), goplsIn))
	p.gopls = jsonrpc2.NewConn(goplsStream)
	p.gopls.Go(ctx, p.handleGoplsNotification)
	return nil
}

func (p *Proxy) handleEditorRequest(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	method := req.Method()

	switch method {
	case "initialize":
		return p.handleInitialize(ctx, reply, req)
	case "textDocument/didOpen":
		return p.handleDidOpen(ctx, reply, req)
	case "textDocument/didChange":
		return p.handleDidChange(ctx, reply, req)
	case "textDocument/didClose":
		return p.handleDidClose(ctx, reply, req)
	case "textDocument/completion":
		return p.handleCompletion(ctx, reply, req)
	case "textDocument/hover":
		return p.handleHover(ctx, reply, req)
	case "textDocument/definition":
		return p.handleDefinition(ctx, reply, req)
	default:
		return p.forward(ctx, reply, req)
	}
}

func (p *Proxy) handleGoplsNotification(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	if req.Method() == "textDocument/publishDiagnostics" {
		return p.handleDiagnostics(ctx, req)
	}
	return p.client.Notify(ctx, req.Method(), req.Params())
}

// forward proxies a request to gopls. Distinguishes requests (expect response)
// from notifications (fire-and-forget) to avoid deadlocks.
func (p *Proxy) forward(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	// Check if this is a notification (no ID, no response expected).
	if _, isNotification := req.(*jsonrpc2.Notification); isNotification {
		return p.gopls.Notify(ctx, req.Method(), req.Params())
	}
	var result json.RawMessage
	_, err := p.gopls.Call(ctx, req.Method(), req.Params(), &result)
	if err != nil {
		return reply(ctx, nil, err)
	}
	return reply(ctx, result, nil)
}

type readWriteCloser struct {
	io.ReadCloser
	io.WriteCloser
}

func newReadWriteCloser(r io.ReadCloser, w io.WriteCloser) io.ReadWriteCloser {
	return &readWriteCloser{r, w}
}

func (rwc *readWriteCloser) Close() error {
	rerr := rwc.ReadCloser.Close()
	werr := rwc.WriteCloser.Close()
	if rerr != nil {
		return rerr
	}
	return werr
}
