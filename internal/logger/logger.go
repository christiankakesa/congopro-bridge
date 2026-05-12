package logger

import (
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func Init(isCLI bool) {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if isCLI {
		cliWriter := zerolog.ConsoleWriter{
			Out: os.Stdout,
			PartsExclude: []string{
				zerolog.TimestampFieldName,
				zerolog.LevelFieldName,
				zerolog.CallerFieldName,
			},
		}
		log.Logger = zerolog.New(cliWriter)
	} else {
		webWriter := zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.RFC3339,
			// Optional: NoColor: true, if you don't want ANSI color codes in your web logs
		}
		log.Logger = zerolog.New(webWriter).With().Timestamp().Logger()
	}
}
