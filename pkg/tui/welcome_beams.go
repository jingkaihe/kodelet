package tui

import (
	"fmt"
	"math/rand/v2"
	"time"
)

const (
	// Advance the effect faster without increasing the terminal's repaint rate.
	welcomeBeamFrameInterval = 10 * time.Millisecond
	welcomeBeamSettleFrame   = welcomeFrames - 16
)

func welcomeBeamGradient(dark bool) [3]string {
	if dark {
		return [3]string{"#8a008a", "#00d1ff", "#ffffff"}
	}
	// White beam tips disappear on light backgrounds; use ink and deeper cyan.
	return [3]string{"#8a008a", "#007c91", "#173b6c"}
}

func (m model) welcomeBeamLogoPalette(y, frame int, finalColor string) *[welcomePaletteSize]string {
	stops := welcomeBeamGradient(m.theme.Dark)
	position := float64(y-welcomeLogoTop) * 2 / float64(welcomeLogoHeight-1)
	c := welcomeBlend(themeColor(stops[2]), themeColor(stops[1]), min(1, position))
	if position > 1 {
		c = welcomeBlend(themeColor(stops[1]), themeColor(stops[0]), position-1)
	}
	settle := float64(min(welcomeFrames-welcomeBeamSettleFrame, max(0, frame-welcomeBeamSettleFrame))) / float64(welcomeFrames-welcomeBeamSettleFrame)
	c = welcomeBlend(c, themeColor(finalColor), settle)
	accent := fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	return cachedWelcomePalette(welcomePaletteKey{accent: accent, highlight: accent, dark: m.theme.Dark})
}

type welcomeBeamHit struct {
	frame  int
	column bool
}

type welcomeBeamSchedule struct {
	hits      [welcomeCanvasHeight][welcomeCanvasWidth][2]welcomeBeamHit
	wipeStart int
}

var welcomeBeams = buildWelcomeBeamSchedule()

func buildWelcomeBeamSchedule() welcomeBeamSchedule {
	type sweep struct {
		index   int
		speed   int
		column  bool
		reverse bool
	}
	rng := rand.New(rand.NewPCG(0x6265616d73, 0x6b6f64656c6574))
	var groups [welcomeCanvasHeight + welcomeCanvasWidth]sweep
	for i := range groups {
		group := sweep{index: i, speed: 15 + rng.IntN(46)}
		if i >= welcomeCanvasHeight {
			group.index = i - welcomeCanvasHeight
			group.column = true
			group.speed = 9 + rng.IntN(7)
		}
		group.reverse = rng.IntN(2) == 0
		groups[i] = group
	}
	rng.Shuffle(len(groups), func(i, j int) {
		groups[i], groups[j] = groups[j], groups[i]
	})
	var schedule welcomeBeamSchedule
	for y := range schedule.hits {
		for x := range schedule.hits[y] {
			schedule.hits[y][x][0].frame = -1
			schedule.hits[y][x][1].frame = -1
		}
	}
	lastHit := 0
	for next, batch := 0, 0; next < len(groups); batch++ {
		remaining := len(groups) - next
		minimum := max(1, remaining-(16-batch)*5)
		count := minimum + rng.IntN(min(5, remaining)-minimum+1)
		for _, group := range groups[next : next+count] {
			length := welcomeCanvasWidth
			if group.column {
				length = welcomeCanvasHeight
			}
			for position, counter, elapsed := 0, 0, 0; position < length; elapsed++ {
				counter += group.speed
				if counter < 20 {
					continue
				}
				advance := min(counter/10, length-position)
				counter -= advance * 10
				for range advance {
					coordinate := position
					if group.reverse {
						coordinate = length - 1 - position
					}
					x, y := coordinate, group.index
					if group.column {
						x, y = group.index, coordinate
					}
					hit := welcomeBeamHit{frame: batch*7 + elapsed, column: group.column}
					hits := &schedule.hits[y][x]
					if hits[0].frame < 0 {
						hits[0] = hit
					} else if hit.frame < hits[0].frame {
						hits[1], hits[0] = hits[0], hit
					} else {
						hits[1] = hit
					}
					lastHit = max(lastHit, hit.frame)
					position++
				}
			}
		}
		next += count
	}
	schedule.wipeStart = lastHit + 42
	return schedule
}

func welcomeBeamCell(x, y, frame int, symbol rune) (rune, int) {
	wipeAge := frame - welcomeBeams.wipeStart - (x+y)/3
	if wipeAge >= 0 {
		return symbol, min(10, wipeAge/4)
	}
	hits := welcomeBeams.hits[y][x]
	hit := hits[0]
	if hits[1].frame <= frame {
		hit = hits[1]
	}
	age := frame - hit.frame
	if age < 0 {
		return ' ', 0
	}
	if age < 20 {
		step := age / 2
		if hit.column {
			glyphs := [...]rune{'▌', '▌', '▌', '▍', '▍', '▍', '▎', '▎', '▏', '▏'}
			return glyphs[step], 20 - step*2
		}
		glyphs := [...]rune{'▂', '▂', '▂', '▂', '▁', '▁', '▁', '_', '_', '_'}
		return glyphs[step], 20 - step*2
	}
	return symbol, max(0, 10-(age-20)/2)
}
