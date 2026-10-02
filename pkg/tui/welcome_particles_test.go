package tui

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWelcomeParticlesFrontSilhouetteAndScale(t *testing.T) {
	particles := newWelcomeParticles()
	for _, height := range []int{welcomeCanvasHeight, welcomeParticleMaxHeight} {
		canvas := rasterWelcomeParticles(particles, 0, height, 0, 0)
		lastBody, firstAccent := 0, canvas.width
		stemBottom, armBottom, accentBottom := -1, -1, -1
		armRows := make([]int, canvas.height)
		for x := range canvas.width {
			for y := range canvas.height {
				ink := canvas.shade[y][x]
				if ink == 0 {
					continue
				}
				if ink <= welcomeSpinShades {
					lastBody = max(lastBody, x)
					if x < canvas.width/3 {
						stemBottom = max(stemBottom, y)
					} else {
						armBottom = max(armBottom, y)
						armRows[y]++
					}
				} else {
					firstAccent = min(firstAccent, x)
					accentBottom = max(accentBottom, y)
				}
			}
		}
		require.Positive(t, stemBottom)
		require.Positive(t, accentBottom)
		assert.GreaterOrEqual(t, firstAccent-lastBody, 2, "preserve the underscore's separation")
		assert.Equal(t, stemBottom, armBottom, "the lower arm shares the stem's baseline at %d rows", height)
		assert.Equal(t, stemBottom, accentBottom, "the underscore shares the stem's baseline at %d rows", height)
		if height == welcomeParticleMaxHeight {
			assert.Less(t, armRows[armBottom], armRows[armBottom-4], "the angled cap tapers instead of being clipped flat")
		}
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
					require.Zero(t, p.drift(0.45), "ambient drift leaves the core intact")
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
		frame := rasterWelcomeParticles(particles, elapsed, 24, yaw, phase)
		scattered := scatterWelcomeParticles(particles, elapsed, welcomeSpinPoint{x: 19.5, y: 12}, yaw, phase)
		assert.Equal(t, before, particles)
		assert.True(t, frame == rasterWelcomeParticles(scattered, 0, 24, yaw, phase), "a click preserves every displayed dot, including ambient drift")
		for _, p := range scattered {
			require.LessOrEqual(t, math.Sqrt(p.velocity.x*p.velocity.x+p.velocity.y*p.velocity.y+p.velocity.z*p.velocity.z), 48.000001, "repeated impulses remain bounded")
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

func BenchmarkWelcomeParticles(b *testing.B) {
	particles := newWelcomeParticles()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		phase := float64(i%180) / 180
		rasterWelcomeParticles(particles, 0, 24, phase*2*math.Pi, phase)
	}
}
