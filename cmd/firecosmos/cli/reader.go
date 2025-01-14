package cli

import (
	"context"
	"os"
	"strings"
	"time"

	dgrpcserver "github.com/streamingfast/dgrpc/server"
	"github.com/streamingfast/node-manager/mindreader"
	"github.com/streamingfast/shutter"
	"go.uber.org/zap"

	"github.com/figment-networks/firehose-cosmos/filereader"
	"github.com/figment-networks/firehose-cosmos/noderunner"
)

type ReaderApp struct {
	*shutter.Shutter

	mode             string
	lineBufferSize   int
	serverListenAddr string
	mrp              *mindreader.MindReaderPlugin
	server           dgrpcserver.Server

	// Node runner options
	nodeBinPath    string
	nodeDir        string
	nodeArgs       string
	nodeEnv        string
	nodeLogsFilter string

	// Log reader options
	logsDir         string
	logsFilePattern string
}

func (app *ReaderApp) Terminated() <-chan struct{} {
	return app.mrp.Terminated()
}

func (app *ReaderApp) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Configure shutdown flow
	app.OnTerminating(func(err error) {
		cancel()
	})

	app.mrp.OnTerminated(func(err error) {
		app.Shutdown(err)
	})

	zlog.Info("starting reader", zap.String("mode", app.mode))
	defer zlog.Info("reader stopped")

	zlog.Info("starting reader blockstream server")
	go app.server.Launch(app.serverListenAddr)

	zlog.Info("starting reader plugin")
	go app.mrp.Launch()

	go func() {
		var err error

		switch app.mode {
		case modeStdin:
			err = app.startFromStdin(ctx)
		case modeNode:
			err = app.startFromNode(ctx)
		case modeLogs:
			err = app.startFromLogs(ctx)
		}

		zlog.Info("event logs reader finished", zap.Error(err))
		app.mrp.Stop()
		app.mrp.Shutdown(err)
	}()

	<-app.Terminated()
	return app.Err()
}

func (app *ReaderApp) startFromStdin(ctx context.Context) error {
	return noderunner.StartLineReader(os.Stdin, app.mrp.LogLine, zlog)
}

func (app *ReaderApp) startFromNode(ctx context.Context) error {
	args := strings.Split(app.nodeArgs, " ")
	env := map[string]string{}

	if app.nodeEnv != "" {
		for _, val := range strings.Split(app.nodeEnv, ",") {
			parts := strings.SplitN(val, "=", 2)
			env[parts[0]] = parts[1]
		}
	}

	runner := noderunner.New(app.nodeBinPath, args, true)
	runner.SetLogger(zlog)
	runner.SetLineReader(app.mrp.LogLine)
	runner.SetDir(app.nodeDir)
	runner.SetEnv(env)
	runner.SetLogFiltering(app.nodeLogsFilter)

	return runner.Start(ctx)
}

func (app *ReaderApp) startFromLogs(ctx context.Context) error {
	reader, err := filereader.NewReader(ctx, zlog, 10*time.Second, 10*time.Second, app.logsFilePattern, app.logsDir)
	if err != nil {
		return err
	}
	defer reader.Close()

	return reader.StartSendingFilesInQueue(app.mrp.LogLine)
}
