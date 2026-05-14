package logger

import (
	"net/http"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"
	"github.com/rs/zerolog/log"
)

func Init(isCLI bool) {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if isCLI {
		log.Logger = zerolog.New(zerolog.ConsoleWriter{
			Out: os.Stdout,
			PartsExclude: []string{
				zerolog.TimestampFieldName,
				zerolog.LevelFieldName,
				zerolog.CallerFieldName,
			},
		})
	} else {
		log.Logger = zerolog.New(zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.RFC3339,
		}).With().Timestamp().Logger()
	}
}

func AccessLogMiddleware(next http.Handler) http.Handler {
	h := hlog.NewHandler(log.Logger)(next)

	h = hlog.RemoteAddrHandler("ip")(h)
	h = hlog.UserAgentHandler("ua")(h)
	h = hlog.RefererHandler("referer")(h)
	h = hlog.RequestIDHandler("req_id", "X-Request-Id")(h)

	h = hlog.AccessHandler(func(r *http.Request, status, size int, duration time.Duration) {
		if r.URL.Path == "/health" {
			return
		}
		hlog.FromRequest(r).Info().
			Str("method", r.Method).
			Stringer("url", r.URL).
			Int("status", status).
			Int("size", size).
			Dur("duration", duration).
			Msg("access")
	})(h)

	return h
}
