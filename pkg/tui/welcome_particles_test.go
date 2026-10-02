package tui

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWelcomeParticlesDeterministicAssembly(t *testing.T) {
	particles := newWelcomeParticles()
	require.Greater(t, len(particles), 2000, "dense enough to fill the largest Braille silhouette")
	require.Less(t, len(particles), 5000, "bounded geometry, not a general mesh engine")
	assert.Equal(t, particles, newWelcomeParticles())
	before := slices.Clone(particles)
	front := rasterWelcomeParticles(particles, 0, 12, 0, 0)
	for _, elapsed := range []time.Duration{-time.Second, welcomeParticleDuration, time.Duration(math.MaxInt64)} {
		assert.True(t, front == rasterWelcomeParticles(particles, elapsed, 12, 0, 0), "new particles start gathered without an initial explosion")
	}
	assert.True(t, front == rasterWelcomeParticles(particles, 0, 12, 2*math.Pi, 1), "a full turn returns to the identical rendered mark")
	assert.NotEqual(t, front.shade, rasterWelcomeParticles(particles, 0, 12, 0.6, 0).shade)
	assert.Equal(t, before, particles, "sampling and rendering cannot mutate model state")
	selected := 0
	for _, p := range particles {
		assert.Zero(t, p.offset)
		assert.Zero(t, p.velocity)
		assert.Less(t, math.Abs(p.home.z), 0.5, "the mark has thin relief, not cube extrusion")
		if p.peel {
			selected++
		}
	}
	assert.InDelta(t, 0.15, float64(selected)/float64(len(particles)), 0.05, "only 10–20%% of outline samples can peel away")
	t.Logf("%d surface samples; %d (%.1f%%) can drift", len(particles), selected, 100*float64(selected)/float64(len(particles)))
}

func TestWelcomeParticleDriftReturnsAndPreservesCore(t *testing.T) {
	particles := newWelcomeParticles()
	maxDrifting := 0
	for frame := range 181 {
		phase := float64(frame) / 180
		drifting := 0
		for _, p := range particles {
			d := p.drift(phase)
			if !p.peel || frame == 0 || frame == 180 {
				require.Zero(t, d)
			}
			if d != (welcomeSpinPoint{}) {
				drifting++
			}
			require.LessOrEqual(t, math.Sqrt(d.x*d.x+d.y*d.y+d.z*d.z), 3.3, "dust stays close to the letter")
		}
		maxDrifting = max(maxDrifting, drifting)
	}
	assert.Greater(t, maxDrifting, len(particles)/10)
	assert.Less(t, maxDrifting, len(particles)/5, "the core remains readable, rather than exploding")
	for _, p := range particles {
		assert.Zero(t, p.drift(-1))
		assert.Zero(t, p.drift(2))
	}
	front := rasterWelcomeParticles(particles, 0, 24, 0, 0)
	drift := rasterWelcomeParticles(particles, 0, 24, 0, 0.45)
	outside := 0
	for y := range front.height {
		for x := range front.width {
			if drift.shade[y][x] != 0 && front.shade[y][x] == 0 {
				outside++
			}
		}
	}
	assert.Greater(t, outside, 30, "small fragments actually leave the silhouette")
	assert.True(t, front == rasterWelcomeParticles(particles, 0, 24, 0, 1))
}

func TestWelcomeParticlesFrontSilhouetteAndScale(t *testing.T) {
	particles := newWelcomeParticles()
	for _, height := range []int{9, 12, 18, 24} {
		canvas := rasterWelcomeParticles(particles, 0, height, 0, 0)
		lastBody, firstAccent, bodyDots, accentDots := 0, canvas.width, 0, 0
		stemBottom, armBottom, accentBottom := -1, -1, -1
		armRows := make([]int, canvas.height)
		runs, previous := 0, false
		for x := range canvas.width {
			occupied := false
			for y := range canvas.height {
				ink := canvas.shade[y][x]
				if ink == 0 {
					continue
				}
				occupied = true
				if ink <= welcomeSpinShades {
					lastBody, bodyDots = max(lastBody, x), bodyDots+1
					if x < canvas.width/3 {
						stemBottom = max(stemBottom, y)
					} else {
						armBottom = max(armBottom, y)
						armRows[y]++
					}
				} else {
					firstAccent, accentDots = min(firstAccent, x), accentDots+1
					accentBottom = max(accentBottom, y)
				}
			}
			if occupied && !previous {
				runs++
			}
			previous = occupied
		}
		assert.Equal(t, 2, runs, "the K and underscore have separate continuous silhouettes at %d rows", height)
		assert.GreaterOrEqual(t, firstAccent-lastBody, 2, "preserve the underscore's separation")
		assert.Greater(t, bodyDots, accentDots*4)
		assert.Greater(t, accentDots, 10)
		assert.Equal(t, stemBottom, armBottom, "the lower arm shares the stem's baseline at %d rows", height)
		assert.Equal(t, stemBottom, accentBottom, "the underscore shares the stem's baseline at %d rows", height)
		if height == welcomeParticleMaxHeight {
			assert.Less(t, armRows[armBottom], armRows[armBottom-4], "the angled cap tapers instead of being clipped flat")
		}
		assert.Equal(t, welcomeParticleWidth(height)*2, canvas.width)
		assert.Equal(t, height*4, canvas.height)
	}
	assert.Equal(t, 39, welcomeParticleWidth(9))
	assert.Equal(t, 39, welcomeParticleWidth(12))
	assert.Equal(t, 59, welcomeParticleWidth(18))
	assert.Equal(t, 78, welcomeParticleWidth(24))
	assert.True(t, rasterWelcomeParticles(particles, 0, 24, 0, 0) == rasterWelcomeParticles(particles, 0, 10000, 0, 0))
	for _, height := range []int{-1, 0} {
		canvas := rasterWelcomeParticles(particles, 0, height, 0, 0)
		assert.Zero(t, canvas.height)
		assert.Zero(t, canvas.shade)
	}
}

func TestWelcomeParticleImpactFollowsRotatedDrift(t *testing.T) {
	particles := newWelcomeParticles()
	for _, yaw := range []float64{0, math.Pi / 2, math.Pi, 4.8} {
		basis := welcomeParticleBasis(yaw)
		for _, impact := range []welcomeSpinPoint{{x: 0, y: 12}, {x: 39, y: 12}, {x: 19.5, y: 0}, {x: 19.5, y: 24}} {
			scattered := scatterWelcomeParticles(particles, 0, impact, yaw, 0.45)
			for _, p := range scattered {
				if !p.peel {
					require.Zero(t, p.velocity, "clicks leave the core intact too")
					continue
				}
				d := p.drift(0.45)
				position := rotateWelcomeParticle(welcomeSpinPoint{p.home.x + d.x, p.home.y + d.y, p.home.z + d.z}, basis)
				x, directionX := welcomeParticleReflect(position.x+19.5, 39)
				y, directionY := welcomeParticleReflect(position.y+12, 24)
				velocity := rotateWelcomeParticle(p.velocity, basis)
				require.Positive(t, velocity.x*directionX*(x-impact.x)+velocity.y*directionY*(y-impact.y), "projected momentum points away from the real clicked position")
			}
		}
	}
}

func TestWelcomeParticlesRapidClicksAndExactReturn(t *testing.T) {
	particles := newWelcomeParticles()
	for i, elapsed := range []time.Duration{0, 50 * time.Millisecond, 250 * time.Millisecond, 1300 * time.Millisecond, welcomeParticleDuration} {
		yaw, phase := float64(i)*0.8, float64(i)/5
		before := slices.Clone(particles)
		scattered := scatterWelcomeParticles(particles, elapsed, welcomeSpinPoint{x: 19.5, y: 12}, yaw, phase)
		assert.Equal(t, before, particles)
		for _, height := range []int{9, 12, 18, 24} {
			frame := rasterWelcomeParticles(particles, elapsed, height, yaw, phase)
			assert.True(t, frame == rasterWelcomeParticles(scattered, 0, height, yaw, phase), "a click preserves every displayed dot, including ambient drift")
		}
		for j, p := range particles {
			assert.Equal(t, p.at(elapsed).offset, scattered[j].offset)
			assert.Equal(t, p.home, scattered[j].home)
			assert.Equal(t, p.normal, scattered[j].normal)
		}
		particles = scattered
	}
	for _, p := range particles {
		assert.Zero(t, p.at(welcomeParticleDuration).offset)
		assert.Zero(t, p.at(welcomeParticleDuration).velocity)
	}
	assert.True(t, rasterWelcomeParticles(newWelcomeParticles(), 0, 24, 0, 0) == rasterWelcomeParticles(particles, welcomeParticleDuration, 24, 2*math.Pi, 1))
}

func TestWelcomeParticleSamplingIsFrameRateIndependent(t *testing.T) {
	particles := scatterWelcomeParticles(newWelcomeParticles(), 0, welcomeSpinPoint{x: 19.5, y: 12}, 0, 0)
	instant := 710 * time.Millisecond
	frame := rasterWelcomeParticles(particles, instant, 12, 0.8, 0.45)
	for _, interval := range []time.Duration{time.Second / 15, time.Second / 30, time.Second / 60} {
		for elapsed := time.Duration(0); elapsed < instant; elapsed += interval {
			rasterWelcomeParticles(particles, elapsed, 12, 0.8, 0.45)
		}
		assert.True(t, frame == rasterWelcomeParticles(particles, instant, 12, 0.8, 0.45))
	}
	for _, p := range particles {
		direct := p.at(500 * time.Millisecond)
		resumed := p.at(200 * time.Millisecond).at(300 * time.Millisecond)
		assert.InDelta(t, direct.offset.x, resumed.offset.x, 1e-12)
		assert.InDelta(t, direct.offset.y, resumed.offset.y, 1e-12)
		assert.InDelta(t, direct.offset.z, resumed.offset.z, 1e-12)
		assert.InDelta(t, direct.velocity.x, resumed.velocity.x, 1e-12)
		assert.InDelta(t, direct.velocity.y, resumed.velocity.y, 1e-12)
		assert.InDelta(t, direct.velocity.z, resumed.velocity.z, 1e-12)
	}
}

func TestWelcomeParticlesStayBounded(t *testing.T) {
	particles := newWelcomeParticles()
	for frame := range 181 {
		phase, yaw := float64(frame)/180, float64(frame)*2*math.Pi/180
		if frame%7 == 0 {
			particles = scatterWelcomeParticles(particles, 7*time.Second/30, welcomeSpinPoint{x: float64(frame % 39), y: float64(frame % 24)}, yaw, phase)
		}
		elapsed := time.Duration(frame%7) * time.Second / 30
		for _, height := range []int{9, 12, 18, 24} {
			canvas := rasterWelcomeParticles(particles, elapsed, height, yaw, phase)
			painted := 0
			for y := range canvas.height {
				for x := range canvas.width {
					if ink := canvas.shade[y][x]; ink != 0 {
						painted++
						require.Positive(t, canvas.depth[y][x])
						require.LessOrEqual(t, ink, uint8(2*welcomeSpinShades))
					}
				}
			}
			require.Positive(t, painted, "even the thin edge-on mark must stay visible")
		}
	}
	for _, p := range particles {
		assert.LessOrEqual(t, math.Sqrt(p.velocity.x*p.velocity.x+p.velocity.y*p.velocity.y+p.velocity.z*p.velocity.z), 48.000001)
	}
}

func BenchmarkWelcomeParticles(b *testing.B) {
	particles := newWelcomeParticles()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		phase := float64(i%180) / 180
		rasterWelcomeParticles(particles, 0, 24, phase*2*math.Pi, phase)
	}
}
