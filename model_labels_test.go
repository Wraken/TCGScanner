package main

import (
	"bufio"
	"image"
	"math"
	"os"
	"strconv"
	"strings"
)

// yoloLabel is one line of a YOLO annotation file: a class id plus a box
// expressed as center and size, normalized to the image dimensions.
type yoloLabel struct {
	cx, cy, w, h float64
}

func (l yoloLabel) rect(imgW, imgH int) image.Rectangle {
	cx, cy := l.cx*float64(imgW), l.cy*float64(imgH)
	bw, bh := l.w*float64(imgW), l.h*float64(imgH)
	return image.Rect(
		int(math.Round(cx-bw/2)), int(math.Round(cy-bh/2)),
		int(math.Round(cx+bw/2)), int(math.Round(cy+bh/2)),
	)
}

func readYOLOLabels(path string) ([]yoloLabel, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var labels []yoloLabel
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 {
			continue
		}
		v := make([]float64, 4)
		for i := range v {
			v[i], err = strconv.ParseFloat(fields[i+1], 64)
			if err != nil {
				return nil, err
			}
		}
		labels = append(labels, yoloLabel{cx: v[0], cy: v[1], w: v[2], h: v[3]})
	}
	return labels, sc.Err()
}
