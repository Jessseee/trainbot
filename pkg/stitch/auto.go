package stitch

import (
	"image"
	"math"
	"time"

	"github.com/jo-m/trainbot/internal/pkg/imutil"
	"github.com/jo-m/trainbot/pkg/pmatch"
	"github.com/rs/zerolog/log"
)

const (
	goodScoreNoMove = 0.99
	goodScoreMove   = 0.95

	maxSeqLen = 800

	dxLowPassFactor = 0.9
)

type Config struct {
	PixelsPerM  float64
	MinSpeedKPH float64
	MaxSpeedKPH float64

	VideoFPS float64
}

func (e *Config) MinPxPerFrame() int {
	return int(e.MinSpeedKPH/3.6*e.PixelsPerM/e.VideoFPS) - 1
}

func (e *Config) MaxPxPerFrame() int {
	return int(e.MaxSpeedKPH/3.6*e.PixelsPerM/e.VideoFPS) + 1
}

type sequence struct {
	// Timestamp of the frame before the first frame in the sequence (frames[-1]).
	startTS time.Time

	// The slices must always have the same length.

	// frame[i] contains the i-th frame.
	// All frames must have the same image size.
	frames []image.Image
	// dx[x] is the pixel offset between frames[i-1] and frames[i].
	// Speed of a frame, in pixels/s is calculated as dx[i]/(ts[i] - ts[i-1]).
	// dx[0] must never be 0.
	dx []int
	// ts[i] is the timestamp of the i-th frame.
	ts []time.Time
}

type AutoStitcher struct {
	c            Config
	minDx, maxDx int

	prevFrameIx int
	// Those are all together zero/nil or not.
	prevFrameTs    time.Time
	prevFrameColor image.Image
	prevFrameGray  *image.Gray

	seq          sequence
	dxAbsLowPass float64
}

func NewAutoStitcher(c Config) *AutoStitcher {
	return &AutoStitcher{
		c:     c,
		minDx: c.MinPxPerFrame(),
		maxDx: c.MaxPxPerFrame(),
	}
}

func findOffset(prev, curr *image.Gray, maxDx int) (dx int, score float64) {
	t0 := time.Now()
	defer log.Trace().Dur("dur", time.Since(t0)).Msg("findOffset() duration")

	if prev.Rect.Size() != curr.Rect.Size() {
		log.Panic().Msg("inconsistent size, this should not happen")
	}

	// Centered crop from prev frame,
	// width is 3x max pixels per frame given by max velocity
	w := maxDx * 3
	// and height 3/4 of frame.
	h := int(float64(prev.Rect.Dy())*3/4 + 1)
	subRect := image.Rect(0, 0, w, h).
		Add(curr.Rect.Min).
		Add(
			curr.Rect.Size().
				Sub(image.Pt(int(w), h)).
				Div(2),
		)
	sub, err := imutil.Sub(prev, subRect)
	if err != nil {
		log.Panic().Err(err).Msg("this should not happen")
	}

	// Centered slice crop from next frame,
	// width is 1x max pixels per frame given by max velocity
	// and height 3/4 of frame.
	w = maxDx
	sliceRect := image.Rect(0, 0, w, h).
		Add(curr.Rect.Min).
		Add(
			curr.Rect.Size().
				Sub(image.Pt(w, h)).
				Div(2),
		)

	slice, err := imutil.Sub(curr, sliceRect)
	if err != nil {
		log.Panic().Err(err).Msg("this should not happen")
	}

	// We expect this x value to be found by the search if the frame has not moved.
	xZero := sliceRect.Min.Sub(subRect.Min).X

	x, _, score := pmatch.SearchGrayC(sub.(*image.Gray), slice.(*image.Gray))
	return x - xZero, score
}

func (r *AutoStitcher) reset() {
	log.Trace().Msg("resetting sequence")

	r.seq = sequence{}
	r.dxAbsLowPass = 0
}

func (r *AutoStitcher) record(prevTs time.Time, frame image.Image, dx int, ts time.Time) {
	if r.seq.startTS.IsZero() {
		r.seq.startTS = prevTs
	}

	r.seq.frames = append(r.seq.frames, frame)
	r.seq.dx = append(r.seq.dx, dx)
	r.seq.ts = append(r.seq.ts, ts)
}

func iabs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}

// TryStitchAndReset tries to stitch any remaining frames and resets the sequence.
func (r *AutoStitcher) TryStitchAndReset() *Train {
	defer r.reset()

	if len(r.seq.dx) == 0 {
		log.Info().Msg("nothing to assemble")
		return nil
	}

	log.Info().Msg("end of sequence")
	train, err := fitAndStitch(r.seq, r.c)
	if err != nil {
		log.Err(err).Msg("unable to fit and stitch sequence")
	}

	return train
}

// Frame adds a frame to the AutoStitcher.
// Will make a deep copy of the frame.
func (r *AutoStitcher) Frame(frameColor image.Image, ts time.Time) *Train {
	t0 := time.Now()
	defer log.Trace().Dur("dur", time.Since(t0)).Msg("Frame() duration")

	// Make copies and convert to gray.
	frameColor = imutil.ToRGBA(frameColor)
	frameGray := imutil.ToGray(frameColor)
	// Make sure we always save the previous frame.
	defer func() {
		r.prevFrameIx++
		r.prevFrameTs = ts
		r.prevFrameColor = frameColor
		r.prevFrameGray = frameGray
	}()

	if r.prevFrameColor == nil {
		// First frame, we skip as we need a previous one to do any processing.
		return nil
	}

	dx, score := findOffset(r.prevFrameGray, frameGray, r.maxDx)
	log.Debug().Int("prevFrameIx", r.prevFrameIx).Int("dx", dx).Float64("score", score).Msg("received frame")

	isActive := len(r.seq.dx) > 0
	if isActive {
		r.dxAbsLowPass = r.dxAbsLowPass*(dxLowPassFactor) + math.Abs(float64(dx))*(1-dxLowPassFactor)

		// Bail out before we use too much memory.
		if len(r.seq.dx) > maxSeqLen {
			return r.TryStitchAndReset()
		}

		// We have reached the end of a sequence.
		if r.dxAbsLowPass < r.c.MinSpeedKPH {
			return r.TryStitchAndReset()
		}

		r.record(r.prevFrameTs, frameColor, dx, ts)
		return nil
	}

	if score >= goodScoreNoMove && iabs(dx) < r.minDx {
		log.Debug().Msg("not moving")
		return nil
	}

	if score >= goodScoreMove && iabs(dx) >= r.minDx && iabs(dx) <= r.maxDx {
		log.Info().Msg("start of new sequence")
		r.record(r.prevFrameTs, frameColor, dx, ts)
		r.dxAbsLowPass = math.Abs(float64(dx))
		return nil
	}

	log.Debug().
		Float64("score", score).
		Float64("goodScoreMove", goodScoreMove).
		Int("dx", dx).
		Int("minDx", r.minDx).
		Int("maxDx", r.maxDx).
		Msg("inconclusive frame")
	return nil
}
