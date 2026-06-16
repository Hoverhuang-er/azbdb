package runewidth

func StringWidth(s string) int {
	width := 0
	for range s {
		width++
	}
	return width
}
