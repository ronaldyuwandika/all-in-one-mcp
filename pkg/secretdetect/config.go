package secretdetect

const Replacement = "[REDACTED]"

type Config struct {
	Replacement       string
	DetectHighEntropy bool
	MinEntropyLength  int
	MinEntropy        float64
}

func DefaultConfig() Config {
	return Config{
		Replacement:       Replacement,
		DetectHighEntropy: true,
		MinEntropyLength:  32,
		MinEntropy:        4.5,
	}
}

func normalizeConfig(config Config) Config {
	defaults := DefaultConfig()
	if config.Replacement == "" {
		config.Replacement = defaults.Replacement
	}
	if config.MinEntropyLength <= 0 {
		config.MinEntropyLength = defaults.MinEntropyLength
	}
	if config.MinEntropy <= 0 {
		config.MinEntropy = defaults.MinEntropy
	}
	return config
}
