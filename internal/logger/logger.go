package logger

import (
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"
	"github.com/rs/zerolog/log"

	"golang.org/x/term"
)

// LogType defines the logging context/format to use.
type LogType int

const (
	// Terminal is for CLI tools: no timestamp, no level, plain human-readable output.
	Terminal LogType = iota
	// Application is for long-running services: timestamp + level + structured fields.
	Application
	// Syslog is for systemd/syslog: no timestamp (syslog adds its own), level included.
	Syslog
	// JSON is for log aggregators (Datadog, ELK, GCP): fully structured JSON output.
	JSON
)

// Options allows optional tuning on top of the LogType defaults.
type Options struct {
	// Level overrides the default log level (default: Info).
	Level zerolog.Level
	// Output overrides the writer (default: os.Stdout, os.Stderr for Syslog).
	Output io.Writer
	// TimeFormat overrides the timestamp format for Application type.
	TimeFormat string
	// CallerEnabled adds file:line caller info (useful for Application/JSON in debug).
	CallerEnabled bool
}

func defaultOptions(t LogType) Options {
	opts := Options{
		Level:      zerolog.InfoLevel,
		TimeFormat: time.RFC3339,
	}
	switch t {
	case Terminal:
		opts.Output = os.Stdout
	case Syslog:
		opts.Output = os.Stderr
	case Application, JSON:
		opts.Output = os.Stdout
	}
	return opts
}

// Init configures the global zerolog logger for the given LogType.
// Optional Options can fine-tune the defaults.
//
// Usage:
//
//	logger.Init(logger.Terminal)
//	logger.Init(logger.Application)
//	logger.Init(logger.JSON, logger.Options{Level: zerolog.DebugLevel})
func Init(t LogType, overrides ...Options) {
	opts := defaultOptions(t)
	if len(overrides) > 0 {
		o := overrides[0]
		if o.Level != zerolog.Disabled {
			opts.Level = o.Level
		}
		if o.Output != nil {
			opts.Output = o.Output
		}
		if o.TimeFormat != "" {
			opts.TimeFormat = o.TimeFormat
		}
		opts.CallerEnabled = o.CallerEnabled
	}

	zerolog.SetGlobalLevel(opts.Level)
	switch t {
	case Terminal:
		log.Logger = zerolog.New(zerolog.ConsoleWriter{
			Out: opts.Output,
			PartsExclude: []string{
				zerolog.TimestampFieldName,
				zerolog.LevelFieldName,
				zerolog.CallerFieldName,
			},
		}).Level(opts.Level)

	case Application:
		cw := zerolog.ConsoleWriter{
			Out:        opts.Output,
			TimeFormat: opts.TimeFormat,
		}
		ctx := zerolog.New(cw).Level(opts.Level).With().Timestamp()
		if opts.CallerEnabled {
			ctx = ctx.Caller()
		}
		log.Logger = ctx.Logger()

	case Syslog:
		cw := zerolog.ConsoleWriter{
			Out:     opts.Output,
			NoColor: true,
			PartsExclude: []string{
				zerolog.TimestampFieldName,
			},
		}
		ctx := zerolog.New(cw).Level(opts.Level).With()
		if opts.CallerEnabled {
			ctx = ctx.Caller()
		}
		log.Logger = ctx.Logger()

	case JSON:
		ctx := zerolog.New(opts.Output).Level(opts.Level).With().Timestamp()
		if opts.CallerEnabled {
			ctx = ctx.Caller()
		}
		log.Logger = ctx.Logger()
	}
}

func InitAuto(overrides ...Options) {
	opts := Options{Level: DetectLogLevel()}
	if len(overrides) > 0 && overrides[0].Level != zerolog.Disabled {
		opts.Level = overrides[0].Level
	}
	Init(DetectLogType(), opts)
}

func DetectLogLevel() zerolog.Level {
	if raw := os.Getenv("LOG_LEVEL"); raw != "" {
		if lvl, err := zerolog.ParseLevel(raw); err == nil {
			return lvl
		}
	}

	return zerolog.InfoLevel
}

func DetectLogType() LogType {
	logType := Application
	switch {
	case term.IsTerminal(int(os.Stdout.Fd())):
		logType = Terminal
	case os.Getenv("JOURNAL_STREAM") != "":
		logType = Syslog
	}

	return logType
}

var skippedPaths = []string{"/api/v1/health", "/favicon.ico", "/fonts/", "/css/", "/images/"}

// AccessLogMiddleware attaches structured HTTP access logging to a handler chain.
// Skips logging for the /api/v1/health endpoint to avoid noise in monitoring dashboards.
func AccessLogMiddleware(next http.Handler) http.Handler {
	h := hlog.AccessHandler(func(r *http.Request, status, size int, duration time.Duration) {
		for _, p := range skippedPaths {
			if r.URL.Path == p || strings.HasPrefix(r.URL.Path, p) {
				return
			}
		}
		hlog.FromRequest(r).Info().
			Str("method", r.Method).
			Stringer("url", r.URL).
			Int("status", status).
			Int("size", size).
			Dur("duration", duration).
			Msg("access")
	})(next)

	h = hlog.RemoteAddrHandler("ip")(h)
	h = hlog.UserAgentHandler("ua")(h)
	h = hlog.RefererHandler("referer")(h)
	h = hlog.RequestIDHandler("req_id", "X-Request-Id")(h)

	h = hlog.NewHandler(log.Logger)(h)
	return h
}
