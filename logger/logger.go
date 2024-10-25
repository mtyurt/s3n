package logger

import (
	"log"
	"os"
)

type logger struct {
	enabled bool
	log     *log.Logger
}

var (
	defaultLogger = &logger{enabled: false, log: log.Default()}
)

func Initialize(file *os.File) {
	defaultLogger.log.SetOutput(file)

	defaultLogger.enabled = true
}
func Println(msg ...interface{}) {
	if defaultLogger.enabled {
		defaultLogger.log.Println(msg...)
	}
}

func Printf(format string, msg ...interface{}) {
	if defaultLogger.enabled {
		defaultLogger.log.Printf(format, msg...)
	}
}
