package tui

import (
	"math"
	"time"
)

const (
	welcomeParticleDuration     = 1500 * time.Millisecond
	welcomeParticleCanvasHeight = 12
	welcomeParticleMaxHeight    = 24
)

// Homes and spring offsets are object-space coordinates centered in the scene.
// A thin, double-sided point cloud gives the mark a little relief without making
// it a solid block. Only a fixed subset of outline points can break away.
type welcomeParticle struct {
	home, normal, outward welcomeSpinPoint
	offset, velocity      welcomeSpinPoint
	damping, frequency    float64
	seed                  uint32
	accent, peel          bool
}

func newWelcomeParticles() []welcomeParticle {
	// The four strokes follow assets/logo.svg, with a lighter underscore moved
	// right so it never joins the lower arm after terminal downsampling.
	strokes := [...][5]float64{
		{16, 15, 16, 49, 6},
		{16, 37, 34, 20, 6},
		{22, 31, 36, 49, 6},
		{45, 49, 57, 49, 4},
	}
	distance := func(x, y float64) float64 {
		nearest := math.Inf(1)
		for _, s := range strokes {
			dx, dy := s[2]-s[0], s[3]-s[1]
			length := math.Hypot(dx, dy)
			along := ((x-s[0])*dx+(y-s[1])*dy)/length - length/2
			across := ((x-s[0])*dy - (y-s[1])*dx) / length
			nearest = min(nearest, max(math.Abs(along)-length/2, math.Abs(across)-s[4]/2))
		}
		return nearest
	}
	particles := make([]welcomeParticle, 0, 4096)
	const step, scale = 0.5, 0.48
	for row := range 78 {
		y := 13.25 + float64(row)*step
		for column := range 94 {
			x := 12.25 + float64(column)*step
			d := distance(x, y)
			if d > 0 {
				continue
			}
			seed := uint32(row*94 + column + 1)
			outward := welcomeSpinPoint{x: distance(x+0.25, y) - distance(x-0.25, y), y: distance(x, y+0.25) - distance(x, y-0.25)}
			if length := math.Hypot(outward.x, outward.y); length > 0 {
				outward.x, outward.y = outward.x/length, outward.y/length
			}
			edge := max(0, 1+d/0.9)
			p := welcomeParticle{
				home:      welcomeSpinPoint{x: (x - 35) * scale, y: (y - 33) * scale},
				outward:   outward,
				damping:   5.5 + welcomeParticleNoise(seed+1),
				frequency: 9 + 2*welcomeParticleNoise(seed+2),
				seed:      seed,
				accent:    x > 42,
				peel:      d > -0.9 && welcomeParticleNoise(seed) < 0.45,
			}
			for _, side := range []float64{-1, 1} {
				p.home.z = side * (0.42 - 0.18*edge*edge)
				p.normal = welcomeSpinPoint{x: outward.x * edge * 0.5, y: outward.y * edge * 0.5, z: side * math.Sqrt(1-edge*edge*0.25)}
				particles = append(particles, p)
			}
			if d > -0.3 {
				p.home.z, p.normal = 0, outward
				particles = append(particles, p)
			}
		}
	}
	return particles
}

func welcomeParticleNoise(seed uint32) float64 {
	seed ^= seed >> 16
	seed *= 0x7feb352d
	seed ^= seed >> 15
	seed *= 0x846ca68b
	seed ^= seed >> 16
	return float64(seed&0xffff) / 65535
}

// at samples the click impulse analytically; dropped frames cannot change its
// path. A smooth final taper brings both velocity and displacement to zero.
func (p welcomeParticle) at(elapsed time.Duration) welcomeParticle {
	if elapsed <= 0 {
		return p
	}
	if elapsed >= welcomeParticleDuration {
		p.offset, p.velocity = welcomeSpinPoint{}, welcomeSpinPoint{}
		return p
	}
	if p.offset == (welcomeSpinPoint{}) && p.velocity == (welcomeSpinPoint{}) {
		return p
	}
	t := elapsed.Seconds()
	sin, cos := math.Sincos(p.frequency * t)
	decay := math.Exp(-p.damping * t)
	envelope, derivative := 1.0, 0.0
	const taper = 0.3
	if remaining := (welcomeParticleDuration - elapsed).Seconds(); remaining < taper {
		u := remaining / taper
		envelope = u * u * (3 - 2*u)
		derivative = -6 * u * (1 - u) / taper
	}
	spring := func(offset, velocity float64) (float64, float64) {
		b := (velocity + p.damping*offset) / p.frequency
		position := decay * (offset*cos + b*sin)
		speed := decay*(b*p.frequency*cos-offset*p.frequency*sin) - p.damping*position
		return envelope * position, envelope*speed + derivative*position
	}
	p.offset.x, p.velocity.x = spring(p.offset.x, p.velocity.x)
	p.offset.y, p.velocity.y = spring(p.offset.y, p.velocity.y)
	p.offset.z, p.velocity.z = spring(p.offset.z, p.velocity.z)
	return p
}

// drift follows a short, staggered arc in object space. The core never moves;
// each selected edge point leaves once and comes home before the cycle ends.
func (p welcomeParticle) drift(phase float64) welcomeSpinPoint {
	if !p.peel || phase <= 0 || phase >= 1 {
		return welcomeSpinPoint{}
	}
	start := 0.12 + 0.3*welcomeParticleNoise(p.seed+3)
	duration := 0.32 + 0.16*welcomeParticleNoise(p.seed+4)
	u := (phase - start) / duration
	if u <= 0 || u >= 1 {
		return welcomeSpinPoint{}
	}
	sin := math.Sin(math.Pi * u)
	envelope := sin * sin
	radius := 1.4 + 1.6*welcomeParticleNoise(p.seed+5)
	sideways := 0.7 * math.Sin(2*math.Pi*u)
	return welcomeSpinPoint{
		x: envelope * (p.outward.x*radius - p.outward.y*sideways),
		y: envelope * (p.outward.y*radius + p.outward.x*sideways),
		z: envelope * (welcomeParticleNoise(p.seed+6) - 0.5) * 2,
	}
}

func welcomeParticleBasis(yaw float64) [3]welcomeSpinPoint {
	// Snap complete turns exactly to the same pose, avoiding floor-rounding
	// speckle from sin(2π). The slight tilt disappears at each front view.
	yaw = math.Remainder(yaw, 2*math.Pi)
	if math.Abs(yaw) < 1e-12 {
		yaw = 0
	}
	sy, cy := math.Sincos(yaw)
	sp, cp := math.Sincos(0.09 * sy)
	return [3]welcomeSpinPoint{{cy, sp * sy, -cp * sy}, {0, cp, sp}, {sy, -sp * cy, cp * cy}}
}

func rotateWelcomeParticle(p welcomeSpinPoint, basis [3]welcomeSpinPoint) welcomeSpinPoint {
	return welcomeSpinPoint{
		x: basis[0].x*p.x + basis[1].x*p.y + basis[2].x*p.z,
		y: basis[0].y*p.x + basis[1].y*p.y + basis[2].y*p.z,
		z: basis[0].z*p.x + basis[1].z*p.y + basis[2].z*p.z,
	}
}

// impact is in the parent's canonical 39×24 camera-space scene. Inverting the
// current rotation makes a click push away from its location even edge-on.
// Snapshotting offsets first preserves continuity on repeated clicks. Drift
// belongs to the separate rotation clock and must not restart with an impulse.
func scatterWelcomeParticles(particles []welcomeParticle, elapsed time.Duration, impact welcomeSpinPoint, yaw, phase float64) []welcomeParticle {
	basis := welcomeParticleBasis(yaw)
	scattered := make([]welcomeParticle, len(particles))
	for i, particle := range particles {
		p := particle.at(elapsed)
		if p.peel {
			d := p.drift(phase)
			position := rotateWelcomeParticle(welcomeSpinPoint{p.home.x + p.offset.x + d.x, p.home.y + p.offset.y + d.y, p.home.z + p.offset.z + d.z}, basis)
			x, directionX := welcomeParticleReflect(position.x+welcomeCanvasWidth/2.0, welcomeCanvasWidth)
			y, directionY := welcomeParticleReflect(position.y+welcomeParticleCanvasHeight, welcomeParticleCanvasHeight*2)
			dx, dy := x-impact.x, y-impact.y
			distance := math.Hypot(dx, dy)
			if distance < 0.001 {
				dy, dx = math.Sincos(2 * math.Pi * welcomeParticleNoise(p.seed))
			} else {
				dx, dy = dx/distance, dy/distance
			}
			kick := (28 + 20*welcomeParticleNoise(p.seed+7)) / (1 + distance/14)
			sideways := (welcomeParticleNoise(p.seed+8) - 0.5) * kick * 0.5
			vx, vy := directionX*(dx*kick-dy*sideways), directionY*(dy*kick+dx*sideways)
			p.velocity.x += basis[0].x*vx + basis[0].y*vy
			p.velocity.y += basis[1].x*vx + basis[1].y*vy
			p.velocity.z += basis[2].x*vx + basis[2].y*vy
			if speed := math.Sqrt(p.velocity.x*p.velocity.x + p.velocity.y*p.velocity.y + p.velocity.z*p.velocity.z); speed > 48 {
				p.velocity.x, p.velocity.y, p.velocity.z = p.velocity.x*48/speed, p.velocity.y*48/speed, p.velocity.z*48/speed
			}
		}
		scattered[i] = p
	}
	return scattered
}

func welcomeParticleWidth(height int) int {
	height = max(0, min(height, welcomeParticleMaxHeight))
	return max(welcomeCanvasWidth, (welcomeCanvasWidth*height+welcomeParticleCanvasHeight-1)/welcomeParticleCanvasHeight)
}

func rasterWelcomeParticles(particles []welcomeParticle, impulseElapsed time.Duration, height int, yaw, drift float64) welcomeSpinCanvas {
	height = max(0, min(height, welcomeParticleMaxHeight))
	width := welcomeParticleWidth(height)
	canvas := welcomeSpinCanvas{width: width * 2, height: height * 4}
	if height == 0 {
		return canvas
	}
	scale := float64(height) / welcomeParticleCanvasHeight
	basis := welcomeParticleBasis(yaw)
	for _, particle := range particles {
		p := particle.at(impulseElapsed)
		d := p.drift(drift)
		displacement := welcomeSpinPoint{p.offset.x + d.x, p.offset.y + d.y, p.offset.z + d.z}
		position := rotateWelcomeParticle(welcomeSpinPoint{p.home.x + displacement.x, p.home.y + displacement.y, p.home.z + displacement.z}, basis)
		normal := rotateWelcomeParticle(p.normal, basis)
		if normal.z < 0 {
			continue
		}
		// Folding excursions avoids both clipped dust and clumps at a border.
		position.x, _ = welcomeParticleReflect(position.x+welcomeCanvasWidth/2.0, welcomeCanvasWidth)
		position.y, _ = welcomeParticleReflect(position.y+welcomeParticleCanvasHeight, welcomeParticleCanvasHeight*2)
		x := int((float64(width)/2 + (position.x-welcomeCanvasWidth/2.0)*scale) * 2)
		y := int(position.y * scale * 2)
		light := max(0, min(1, -0.2*normal.x-0.35*normal.y+0.91*normal.z))
		shade := uint8(1 + math.Round(float64(welcomeSpinShades-1)*(0.25+0.75*light)))
		detached := displacement.x*displacement.x+displacement.y*displacement.y+displacement.z*displacement.z > 0.16
		canvas.plot(x, y, float32(32+position.z), shade, p.accent || detached)
	}
	return canvas
}

func welcomeParticleReflect(value, size float64) (float64, float64) {
	const margin = 0.25
	if value >= margin && value <= size-margin {
		return value, 1
	}
	span := size - 2*margin
	folded := math.Mod(value-margin, 2*span)
	if folded < 0 {
		folded += 2 * span
	}
	if folded <= span {
		return margin + folded, 1
	}
	return size - margin - (folded - span), -1
}
