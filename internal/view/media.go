package view

import "fmt"

// PercentageCount formats a percentage together with the values behind it. An empty sample is not called 0%.
func PercentageCount(done, total int) string {
	if total == 0 {
		return "n/a (0/0)"
	}
	return fmt.Sprintf("%.1f%% (%d/%d)", 100*float64(done)/float64(total), done, total)
}
