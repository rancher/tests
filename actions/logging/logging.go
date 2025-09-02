package logging

import "github.com/sirupsen/logrus"

const (
	LoggingKey = "logging"
)

type Logging struct {
	Level *string `json:"level,omitempty" yaml:"level,omitempty"`
}

func SetLogger(loggingConfig *Logging) error {
	var loggingLevel logrus.Level
	var err error
	if loggingConfig != nil {
		if loggingConfig.Level != nil {
			loggingLevel, err = logrus.ParseLevel(*loggingConfig.Level)
			if err != nil {
				return err
			}

			logrus.SetLevel(loggingLevel)
		}
	} else {
		loggingLevel = logrus.InfoLevel
		logrus.SetLevel(loggingLevel)
	}

	setLogrusFormatter()
	logrus.Infof("Setting logging level to: %s", loggingLevel.String())

	return nil
}

func setLogrusFormatter() {
	formatter := &logrus.TextFormatter{}
	formatter.DisableQuote = true
	formatter.TimestampFormat = "2006-01-02 15:04:05"
	formatter.FullTimestamp = true
	formatter.ForceColors = true
	formatter.PadLevelText = true
	formatter.DisableLevelTruncation = true
	logrus.SetFormatter(formatter)
}
