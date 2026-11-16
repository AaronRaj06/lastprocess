package render

// Screen is a minimal terminal emulator: it understands exactly the sequences
// Full and Diff emit (cursor position, clear, SGR) plus plain bytes. Tests use
// it to prove a diff stream reproduces the frames, and the load tester uses it
// to read the HUD the way a player's terminal would show it.
type Screen struct {
	W, H int
	Rows [][]byte
	x, y int
	esc  []byte // bytes of an unfinished escape sequence
}

// NewScreen returns a blank w x h screen.
func NewScreen(w, h int) *Screen {
	s := &Screen{W: w, H: h, Rows: make([][]byte, h)}
	s.clear()
	return s
}

func (s *Screen) clear() {
	for y := range s.Rows {
		s.Rows[y] = make([]byte, s.W)
		for x := range s.Rows[y] {
			s.Rows[y][x] = ' '
		}
	}
}

// Write feeds terminal output to the screen. It never fails.
func (s *Screen) Write(p []byte) (int, error) {
	for _, b := range p {
		switch {
		case len(s.esc) > 0:
			s.esc = append(s.esc, b)
			if len(s.esc) > 2 && b >= 0x40 && b <= 0x7e { // final byte of a CSI sequence
				s.csi(s.esc[2:len(s.esc)-1], b)
				s.esc = s.esc[:0]
			} else if len(s.esc) == 2 && b != '[' {
				s.esc = s.esc[:0] // not a CSI sequence; ignore
			}
		case b == 0x1b:
			s.esc = append(s.esc, b)
		case b >= 0x20 && s.y < s.H && s.x < s.W:
			s.Rows[s.y][s.x] = b
			s.x++
		}
	}
	return len(p), nil
}

func (s *Screen) csi(params []byte, final byte) {
	switch final {
	case 'H':
		var n [2]int // row;col, 1-based, missing means 1
		i := 0
		for _, c := range params {
			if c == ';' && i == 0 {
				i = 1
			} else if c >= '0' && c <= '9' {
				n[i] = n[i]*10 + int(c-'0')
			}
		}
		n[0], n[1] = max(n[0], 1), max(n[1], 1)
		s.y, s.x = n[0]-1, n[1]-1
	case 'J':
		s.clear()
	}
}
