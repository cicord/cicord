package paint

import (
	"bufio"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"io"
)

// encodePNG writes dst as an 8-bit PNG with the "Up" filter on every row. The
// standard encoder tries the five filters on each row and keeps the best, which
// took half of the render time inside the worker; one fixed filter compresses
// cards almost as well for a fraction of the work.
func encodePNG(w io.Writer, dst *image.RGBA) error {
	bw := bufio.NewWriterSize(w, 32<<10)
	opaque := dst.Opaque()
	channels, colorType := 4, byte(6)
	if opaque {
		channels, colorType = 3, 2
	}
	width, height := dst.Rect.Dx(), dst.Rect.Dy()
	if _, err := bw.WriteString("\x89PNG\r\n\x1a\n"); err != nil {
		return err
	}
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header[0:], uint32(width))  //nolint:gosec // G115: at most MaxSide
	binary.BigEndian.PutUint32(header[4:], uint32(height)) //nolint:gosec // G115: at most MaxSide
	header[8], header[9] = 8, colorType
	if err := chunk(bw, "IHDR", header); err != nil {
		return err
	}
	data := &chunkWriter{w: bw, name: "IDAT"}
	z, err := zlib.NewWriterLevel(data, zlib.BestSpeed)
	if err != nil {
		return err
	}
	stride := width * channels
	prev, cur := make([]byte, stride), make([]byte, stride)
	line := make([]byte, 1+stride)
	line[0] = 2 // Up
	for y := range height {
		row := dst.Pix[(y)*dst.Stride : (y)*dst.Stride+4*width]
		for x := range width {
			p := row[4*x : 4*x+4]
			c := cur[channels*x : channels*x+channels]
			if opaque {
				c[0], c[1], c[2] = p[0], p[1], p[2]
				continue
			}
			// Un-premultiply exactly as color.NRGBAModel does.
			r, g, b, a := uint32(p[0])*0x101, uint32(p[1])*0x101, uint32(p[2])*0x101, uint32(p[3])*0x101
			switch a {
			case 0:
				c[0], c[1], c[2], c[3] = 0, 0, 0, 0
			case 0xffff:
				c[0], c[1], c[2], c[3] = p[0], p[1], p[2], 255
			default:
				// Premultiplied channels never exceed alpha, so each result fits in 8 bits.
				c[0], c[1], c[2], c[3] = uint8((r*0xffff/a)>>8), uint8((g*0xffff/a)>>8), uint8((b*0xffff/a)>>8), uint8(a>>8) //nolint:gosec // G115: see above
			}
		}
		for i := range cur {
			line[1+i] = cur[i] - prev[i]
		}
		if _, err := z.Write(line); err != nil {
			return err
		}
		prev, cur = cur, prev
	}
	if err := z.Close(); err != nil {
		return err
	}
	if err := data.flush(); err != nil {
		return err
	}
	if err := chunk(bw, "IEND", nil); err != nil {
		return err
	}
	return bw.Flush()
}

func chunk(w io.Writer, name string, data []byte) error {
	var head [8]byte
	binary.BigEndian.PutUint32(head[:4], uint32(len(data))) //nolint:gosec // G115: chunks hold at most 32 KiB
	copy(head[4:], name)
	crc := crc32.NewIEEE()
	crc.Write(head[4:])
	crc.Write(data)
	var tail [4]byte
	binary.BigEndian.PutUint32(tail[:], crc.Sum32())
	for _, b := range [][]byte{head[:], data, tail[:]} {
		if _, err := w.Write(b); err != nil {
			return err
		}
	}
	return nil
}

// chunkWriter splits the compressed stream into IDAT chunks of up to 32 KiB.
type chunkWriter struct {
	w    io.Writer
	name string
	buf  []byte
}

func (c *chunkWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		room := 32<<10 - len(c.buf)
		take := min(room, len(p))
		c.buf = append(c.buf, p[:take]...)
		p = p[take:]
		if len(c.buf) == 32<<10 {
			if err := c.flush(); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

func (c *chunkWriter) flush() error {
	if len(c.buf) == 0 {
		return nil
	}
	err := chunk(c.w, c.name, c.buf)
	c.buf = c.buf[:0]
	return err
}
