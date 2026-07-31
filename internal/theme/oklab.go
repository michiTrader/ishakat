package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Los degradados se interpolan en espacio perceptual y no en RGB lineal, porque
// en RGB los pasos intermedios se ven sucios y grisáceos: mezclar #ff6a3d con
// #ffe0a3 en RGB pasa por un ocre apagado, y en Oklab pasa por el ámbar que uno
// esperaba. Son treinta líneas de matemática y la diferencia se ve a simple
// vista en la primera línea del banner.

// RGB es un color con componentes de 0 a 255.
type RGB struct{ R, G, B uint8 }

// Hex devuelve el color en notación #rrggbb.
func (c RGB) Hex() string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// ParseHex acepta #rgb, #rrggbb y rrggbb.
func ParseHex(s string) (RGB, error) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	switch len(s) {
	case 3:
		var out RGB
		for i, p := range []*uint8{&out.R, &out.G, &out.B} {
			v, err := strconv.ParseUint(s[i:i+1], 16, 8)
			if err != nil {
				return RGB{}, fmt.Errorf("theme: color inválido %q", s)
			}
			*p = uint8(v*16 + v)
		}
		return out, nil
	case 6:
		v, err := strconv.ParseUint(s, 16, 32)
		if err != nil {
			return RGB{}, fmt.Errorf("theme: color inválido %q", s)
		}
		return RGB{uint8(v >> 16), uint8(v >> 8 & 0xff), uint8(v & 0xff)}, nil
	}
	return RGB{}, fmt.Errorf("theme: color inválido %q", s)
}

// oklab es el color en el espacio perceptual de Björn Ottosson.
type oklab struct{ L, A, B float64 }

func srgbToLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func linearToSRGB(c float64) float64 {
	if c <= 0.0031308 {
		return c * 12.92
	}
	return 1.055*math.Pow(c, 1.0/2.4) - 0.055
}

func toOklab(c RGB) oklab {
	r := srgbToLinear(float64(c.R) / 255)
	g := srgbToLinear(float64(c.G) / 255)
	b := srgbToLinear(float64(c.B) / 255)

	l := 0.4122214708*r + 0.5363325363*g + 0.0514459929*b
	m := 0.2119034982*r + 0.6806995451*g + 0.1073969566*b
	s := 0.0883024619*r + 0.2817188376*g + 0.6299787005*b

	l, m, s = math.Cbrt(l), math.Cbrt(m), math.Cbrt(s)

	return oklab{
		L: 0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		A: 1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		B: 0.0259040371*l + 0.7827717662*m - 0.8086757660*s,
	}
}

func (c oklab) toRGB() RGB {
	l := c.L + 0.3963377774*c.A + 0.2158037573*c.B
	m := c.L - 0.1055613458*c.A - 0.0638541728*c.B
	s := c.L - 0.0894841775*c.A - 1.2914855480*c.B

	l, m, s = l*l*l, m*m*m, s*s*s

	r := +4.0767416621*l - 3.3077115913*m + 0.2309699292*s
	g := -1.2684380046*l + 2.6097574011*m - 0.3413193965*s
	b := -0.0041960863*l - 0.7034186147*m + 1.7076147010*s

	return RGB{clamp8(linearToSRGB(r)), clamp8(linearToSRGB(g)), clamp8(linearToSRGB(b))}
}

func clamp8(f float64) uint8 {
	v := f*255 + 0.5
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v)
}

// Space es el espacio en que se interpola un degradado.
type Space string

const (
	SpaceOklab Space = "oklab"
	SpaceOklch Space = "oklch"
	SpaceHSL   Space = "hsl"
)

// Mix interpola dos colores en el espacio indicado, con t entre 0 y 1.
func Mix(a, b RGB, t float64, space Space) RGB {
	t = clamp01(t)
	switch space {
	case SpaceOklch:
		return mixOklch(a, b, t)
	case SpaceHSL:
		return mixHSL(a, b, t)
	default:
		x, y := toOklab(a), toOklab(b)
		return oklab{
			L: lerp(x.L, y.L, t),
			A: lerp(x.A, y.A, t),
			B: lerp(x.B, y.B, t),
		}.toRGB()
	}
}

func mixOklch(a, b RGB, t float64) RGB {
	x, y := toOklab(a), toOklab(b)
	cx, hx := math.Hypot(x.A, x.B), math.Atan2(x.B, x.A)
	cy, hy := math.Hypot(y.A, y.B), math.Atan2(y.B, y.A)
	// Camino corto en el círculo de tono.
	d := hy - hx
	if d > math.Pi {
		d -= 2 * math.Pi
	}
	if d < -math.Pi {
		d += 2 * math.Pi
	}
	l := lerp(x.L, y.L, t)
	c := lerp(cx, cy, t)
	h := hx + d*t
	return oklab{L: l, A: c * math.Cos(h), B: c * math.Sin(h)}.toRGB()
}

func mixHSL(a, b RGB, t float64) RGB {
	h1, s1, l1 := toHSL(a)
	h2, s2, l2 := toHSL(b)
	d := h2 - h1
	if d > 180 {
		d -= 360
	}
	if d < -180 {
		d += 360
	}
	return fromHSL(h1+d*t, lerp(s1, s2, t), lerp(l1, l2, t))
}

func toHSL(c RGB) (h, s, l float64) {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	max := math.Max(r, math.Max(g, b))
	min := math.Min(r, math.Min(g, b))
	l = (max + min) / 2
	if max == min {
		return 0, 0, l
	}
	d := max - min
	if l > 0.5 {
		s = d / (2 - max - min)
	} else {
		s = d / (max + min)
	}
	switch max {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h * 60, s, l
}

func fromHSL(h, s, l float64) RGB {
	h = math.Mod(math.Mod(h, 360)+360, 360) / 360
	if s == 0 {
		v := clamp8(l)
		return RGB{v, v, v}
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	return RGB{clamp8(hue(p, q, h+1.0/3)), clamp8(hue(p, q, h)), clamp8(hue(p, q, h-1.0/3))}
}

func hue(p, q, t float64) float64 {
	if t < 0 {
		t++
	}
	if t > 1 {
		t--
	}
	switch {
	case t < 1.0/6:
		return p + (q-p)*6*t
	case t < 1.0/2:
		return q
	case t < 2.0/3:
		return p + (q-p)*(2.0/3-t)*6
	}
	return p
}

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

func clamp01(t float64) float64 {
	if t < 0 {
		return 0
	}
	if t > 1 {
		return 1
	}
	return t
}

// Ramp devuelve n colores repartidos por la escala de paradas, interpolando en
// el espacio dado. Con n == 1 devuelve la primera parada.
func Ramp(stops []RGB, n int, space Space) []RGB {
	if n <= 0 || len(stops) == 0 {
		return nil
	}
	if len(stops) == 1 || n == 1 {
		out := make([]RGB, n)
		for i := range out {
			out[i] = stops[0]
		}
		return out
	}
	out := make([]RGB, n)
	for i := range out {
		t := float64(i) / float64(n-1)
		seg := t * float64(len(stops)-1)
		idx := int(seg)
		if idx >= len(stops)-1 {
			idx = len(stops) - 2
		}
		out[i] = Mix(stops[idx], stops[idx+1], seg-float64(idx), space)
	}
	return out
}
