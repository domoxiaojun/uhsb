package cfdata

import (
	"net"
	"regexp"
	"strings"
)

var coloLineRegex = regexp.MustCompile(`[,\s]+`)

func parseLines(content string) []string {
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func validateSubnetLines(lines []string) error {
	for _, line := range lines {
		if _, _, err := net.ParseCIDR(line); err != nil {
			return err
		}
	}
	return nil
}

func ParseColoList(input string) []string {
	parts := coloLineRegex.Split(strings.TrimSpace(input), -1)
	var result []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			result = append(result, strings.ToUpper(p))
		}
	}
	return result
}

func SanitizeOutputTag(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "results"
	}

	var b strings.Builder
	lastUnderscore := false
	for _, r := range tag {
		isSafe := r == '_' || r == '-' || r == '.' ||
			(r >= '0' && r <= '9') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= 'a' && r <= 'z')
		if isSafe {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}

	safe := strings.Trim(b.String(), "_.")
	if safe == "" {
		return "results"
	}
	return safe
}

func ShouldStopForTargetColos(counts map[string]int, targets []string, numPerColo int) bool {
	if numPerColo <= 0 || len(targets) == 0 {
		return false
	}
	for _, target := range targets {
		if counts[strings.ToUpper(target)] < numPerColo {
			return false
		}
	}
	return true
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	return a
}
