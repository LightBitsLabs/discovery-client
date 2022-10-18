package logging

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"runtime"
	"time"

	"github.com/lightbitslabs/discovery-client/pkg/collections"
	"github.com/rifflock/lfshook"
	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	validLevels = []string{"debug", "info", "warn", "warning", "error", "fatal"}
)

type Config struct {
	// Write to file? if not provided not writing to file
	Filename string `yaml:"filename,omitempty"`
	// Time to wait until old logs are purged. By default no logs are purged
	MaxAge time.Duration `yaml:"maxAge,omitempty"`
	// MaxSize is the maximum size of the file in MB
	MaxSize int `yaml:"maxSize,omitempty"`
	// Write caller file:line and package.function on log entries
	ReportCaller bool `yaml:"reportCaller,omitempty"`
	// one of trace, debug, info, warn, warning, error, fatal, panic
	Level string `yaml:"level,omitempty"`
}

func (c *Config) IsValid() error {
	if !collections.Include(validLevels, c.Level) {
		return fmt.Errorf("invalid logging.level parameter provided. supported levels: %v, provided: %s", validLevels, c.Level)
	}
	return nil
}

func setupConsoleLogs(disableTimeStamp bool) {
	writerMap := lfshook.WriterMap{}
	for level := int(logrus.InfoLevel); level > int(logrus.PanicLevel); level-- {
		writerMap[logrus.Level(level)] = os.Stdout
	}

	textFormatter := &logrus.TextFormatter{
		DisableColors:    true,
		DisableTimestamp: disableTimeStamp,
		FullTimestamp:    !disableTimeStamp,
		CallerPrettyfier: func(f *runtime.Frame) (string, string) {
			_, filename := path.Split(f.File)
			return path.Base(f.Function), fmt.Sprintf("%s:%d", filename, f.Line)
		},
	}

	hook := lfshook.NewHook(
		writerMap,
		textFormatter,
	)

	logrus.AddHook(hook)
}

func setupLoggingFile(cfg Config, wantedLevel logrus.Level) error {
	textFormatter := &logrus.TextFormatter{
		DisableColors: true,
		FullTimestamp: true,
		CallerPrettyfier: func(f *runtime.Frame) (string, string) {
			_, filename := path.Split(f.File)
			return path.Base(f.Function), fmt.Sprintf("%s:%d", filename, f.Line)
		},
	}

	logrus.SetReportCaller(cfg.ReportCaller)
	logrus.SetLevel(wantedLevel)

	if len(cfg.Filename) > 0 {
		writer := &lumberjack.Logger{
			Filename:  cfg.Filename,
			MaxSize:   cfg.MaxSize,
			Compress:  true,
			MaxAge:    int(cfg.MaxAge),
			LocalTime: false,
		}

		writerMap := lfshook.WriterMap{}
		for level := int(wantedLevel); level > int(logrus.PanicLevel); level-- {
			writerMap[logrus.Level(level)] = writer
		}
		hook := lfshook.NewHook(
			writerMap,
			textFormatter,
		)
		logrus.AddHook(hook)
	}
	return nil
}

func SetupLogging(cfg Config) error {
	var err error
	wantedLevel := logrus.InfoLevel
	if len(cfg.Level) > 0 {
		wantedLevel, err = logrus.ParseLevel(cfg.Level)
		if err != nil {
			return err
		}
	}

	logrus.SetOutput(ioutil.Discard)
	disableTimeStamp := true
	setupConsoleLogs(disableTimeStamp)
	setupLoggingFile(cfg, wantedLevel)

	return nil
}

func SetupLoggingWithConsoleTimeStamp(cfg Config) error {
	var err error
	wantedLevel := logrus.InfoLevel
	if len(cfg.Level) > 0 {
		wantedLevel, err = logrus.ParseLevel(cfg.Level)
		if err != nil {
			return err
		}
	}

	logrus.SetOutput(ioutil.Discard)
	disableTimeStamp := false
	setupConsoleLogs(disableTimeStamp)
	setupLoggingFile(cfg, wantedLevel)

	return nil
}
