package privacy

import (
	"regexp"
	"strings"

	"github.com/wellivea1/server-status/internal/config"
	"github.com/wellivea1/server-status/internal/models"
)

type Redactor struct {
	cfg      config.PrivacyConfig
	patterns []*regexp.Regexp
}

type Result struct {
	Text    string
	Changed bool
}

func NewRedactor(cfg config.PrivacyConfig) *Redactor {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)[^\s]+`),
		regexp.MustCompile(`(?i)(api[_-]?key\s*[:=]\s*)[^\s"']+`),
		regexp.MustCompile(`(?i)(token\s*[:=]\s*)[^\s"']+`),
		regexp.MustCompile(`(?i)(password\s*[:=]\s*)[^\s"']+`),
		regexp.MustCompile(`(?i)(cookie\s*[:=]\s*)[^\r\n\s"']+`),
		regexp.MustCompile(`(?i)(session[_-]?id\s*[:=]\s*)[^\s"']+`),
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	}
	if cfg.RedactEmails {
		patterns = append(patterns, regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`))
	}
	if cfg.RedactIPs {
		patterns = append(patterns, regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`))
	}
	for _, glob := range cfg.BlacklistEnvNames {
		if rx := envNamePattern(glob); rx != nil {
			patterns = append(patterns, rx)
		}
	}
	for _, glob := range cfg.BlacklistFilenameGlobs {
		if rx := filenamePattern(glob); rx != nil {
			patterns = append(patterns, rx)
		}
	}
	for _, raw := range cfg.BlacklistLogPatterns {
		if rx, err := regexp.Compile(raw); err == nil {
			patterns = append(patterns, rx)
		}
	}
	return &Redactor{cfg: cfg, patterns: patterns}
}

func (r *Redactor) RedactString(input string) Result {
	out := input
	for _, pattern := range r.patterns {
		out = pattern.ReplaceAllStringFunc(out, func(match string) string {
			if strings.Contains(match, ":") || strings.Contains(match, "=") {
				if idx := strings.IndexAny(match, ":="); idx >= 0 {
					return match[:idx+1] + "[REDACTED]"
				}
			}
			return "[REDACTED]"
		})
	}
	for _, term := range r.terms() {
		if term == "" {
			continue
		}
		out = replaceAllFold(out, term, "[REDACTED]")
	}
	return Result{Text: out, Changed: out != input}
}

func (r *Redactor) RedactLogs(lines []models.LogLine) ([]models.LogLine, bool) {
	out := make([]models.LogLine, 0, len(lines))
	changed := false
	for _, line := range lines {
		source := r.RedactString(line.Source)
		text := r.RedactString(line.Line)
		changed = changed || source.Changed || text.Changed
		line.Source = source.Text
		line.Line = text.Text
		out = append(out, line)
	}
	return out, changed
}

func (r *Redactor) IsBlacklistedApp(app models.AppStatus) bool {
	return containsFold(r.cfg.BlacklistAppIDs, app.AppID) ||
		containsFold(r.cfg.BlacklistContainerNames, app.ContainerName) ||
		containsFold(r.cfg.BlacklistDisplayNames, app.DisplayName)
}

func (r *Redactor) terms() []string {
	var terms []string
	terms = append(terms, r.cfg.BlacklistAppIDs...)
	terms = append(terms, r.cfg.BlacklistContainerNames...)
	terms = append(terms, r.cfg.BlacklistDisplayNames...)
	terms = append(terms, r.cfg.BlacklistFolderPaths...)
	terms = append(terms, r.cfg.BlacklistShareNames...)
	terms = append(terms, r.cfg.BlacklistFilePaths...)
	terms = append(terms, r.cfg.BlacklistURLPatterns...)
	terms = append(terms, r.cfg.BlacklistHostnames...)
	terms = append(terms, r.cfg.BlacklistIPs...)
	terms = append(terms, r.cfg.BlacklistUsernames...)
	return terms
}

func containsFold(values []string, needle string) bool {
	for _, value := range values {
		if strings.EqualFold(value, needle) {
			return true
		}
	}
	return false
}

func envNamePattern(glob string) *regexp.Regexp {
	glob = strings.TrimSpace(glob)
	if glob == "" {
		return nil
	}
	name := strings.ReplaceAll(regexp.QuoteMeta(glob), `\*`, `[A-Za-z0-9_]*`)
	return regexp.MustCompile(`(?i)\b` + name + `\s*[:=]\s*[^\s"']+`)
}

func filenamePattern(glob string) *regexp.Regexp {
	glob = strings.TrimSpace(glob)
	if glob == "" {
		return nil
	}
	name := strings.ReplaceAll(regexp.QuoteMeta(glob), `\*`, `[^\\/\s]*`)
	return regexp.MustCompile(`(?i)(?:^|[\\/])` + name + `(?:\s|$)`)
}

func replaceAllFold(input, term, replacement string) string {
	pattern := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(term))
	return pattern.ReplaceAllString(input, replacement)
}
