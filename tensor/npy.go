package tensor

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"github.com/sugarme/gotch"
)

const (
	NpyMagicString string = `\x93NUMPY`
	NpySuffix      string = ".npy"
)

func readHeader(r io.Reader) (string, error) {
	magicStr := make([]byte, len(NpyMagicString))
	_, err := io.ReadFull(r, magicStr)
	if err != nil {
		return "", err
	}

	if string(magicStr) != NpyMagicString {
		err = fmt.Errorf("magic string mismatched.\n")
		return "", err
	}

	version := make([]byte, 2)

	_, err = io.ReadFull(r, version)
	if err != nil {
		return "", err
	}

	var headerLenLength int
	switch version[0] {
	case 1:
		headerLenLength = 2
	case 2:
		headerLenLength = 4
	default:
		err = fmt.Errorf("Unsupported version: %v\n", version[0])
	}

	headerLen := make([]byte, headerLenLength)

	_, err = io.ReadFull(r, headerLen)
	if err != nil {
		return "", err
	}

	var hLen int = 0
	for i := len(headerLen); i > 0; i-- {
		hLen = hLen*256 + int(headerLen[i])
	}

	header := make([]byte, hLen)
	_, err = io.ReadFull(r, header)
	if err != nil {
		return "", err
	}

	return string(header), nil
}

type Header struct {
	descr        gotch.DType
	fortranOrder bool
	shape        []int64
}

func (h *Header) toString() (string, error) {
	var fortranOrder string = "False"

	if h.fortranOrder {
		fortranOrder = "True"
	}

	var shapeStr []string
	for _, v := range h.shape {
		shapeStr = append(shapeStr, string(v))
	}

	shape := strings.Join(shapeStr, ",")

	var descr string
	switch h.descr.Kind().String() {
	// case "float32":
	// descr = "f2"
	case "float32":
		descr = "f4"
	case "float64":
		descr = "f8"
	case "int":
		descr = "i4"
	case "int64":
		descr = "i8"
	case "int16":
		descr = "i2"
	case "int8":
		descr = "i1"
	case "uint8":
		descr = "u1"
	default:
		err := fmt.Errorf("Unsupported kind: %v\n", h.descr)
		return "", err
	}

	if len(shape) > 0 {
		shape = shape + ","
	}

	headStr := fmt.Sprintf("{'descr': '<%v', 'fortran_order': %v, 'shape': (%v), }", descr, fortranOrder, shape)

	return headStr, nil
}

// Parser for the npy header, a typical example would be:
// {'descr': '<f8', 'fortran_order': False, 'shape': (128,), }
func (h *Header) parse(header string) (*Header, error) {

	// trim matches
	var chars []rune
	for _, r := range header {
		if r == '{' || r == '}' || r == ',' || r == ' ' {
			continue
		}

		chars = append(chars, r)
	}

	trimHeader := string(chars)

	var parts []string
	startIdx := 0
	var cntParenthesis int64 = 0
	for i, r := range trimHeader {
		switch r {
		case '(':
			cntParenthesis += 1
		case ')':
			cntParenthesis -= 1
		case ',':
			if cntParenthesis == 0 {
				parts = append(parts, trimHeader[startIdx:i])
				startIdx = i + 1
			}
		default:
			// do nothing
		}
	}

	parts = append(parts, header[startIdx:])

	var partMap map[string]string = make(map[string]string)

	for _, part := range parts {
		strings.TrimSpace(part)
		p := strings.TrimSpace(part)
		if len(p) > 0 {
			kv := strings.Split(p, ":")
			if len(kv) == 2 {
				var key, value string
				rKey := []rune(kv[0])
				for _, r := range rKey {
					if r == '\'' || r == ' ' {
						continue
					}
					key = key + string([]rune{r})
				}

				rValue := []rune(kv[1])
				for _, r := range rValue {
					if r == '\'' || r == ' ' {
						continue
					}
					value = value + string([]rune{r})
				}

				partMap[key] = value
			}
		}
	}

	var fortranOrder bool
	fo, ok := partMap["fortran_order"]
	if !ok {
		fortranOrder = false
	}
	switch fo {
	case "False":
		fortranOrder = false
	case "True":
		fortranOrder = true
	default:
		err := fmt.Errorf("unknown fortran_order: %v\n", fo)
		return nil, err
	}

	d, ok := partMap["descr"]
	if !ok {
		err := fmt.Errorf("no descr in header.\n")
		return nil, err
	}

	if len(d) == 0 {
		err := fmt.Errorf("empty descr.\n")
		return nil, err
	}

	if strings.HasPrefix(d, ">") {
		err := fmt.Errorf("little-endian descr: %v\n", d)
		return nil, err
	}

	var descrStr string
	for _, r := range d {
		if r == '=' || r == '<' {
			continue
		}
		descrStr += string([]rune{r})
	}

	var descr gotch.DType
	switch descrStr {
	case "f2":
		descr = gotch.Float // use Go float32 as there's no float16
	case "f4":
		descr = gotch.Float
	case "f8":
		descr = gotch.Double
	case "i4":
		descr = gotch.Int
	case "i8":
		descr = gotch.Int64
	case "i2":
		descr = gotch.Int16
	case "i1":
		descr = gotch.Int8
	case "u1":
		descr = gotch.Uint8
	default:
		err := fmt.Errorf("unrecognized descr: %v\n", descr)
		return nil, err
	}

	s, ok := partMap["shape"]
	if !ok {
		err := fmt.Errorf("no shape in header.\n")
		return nil, err
	}

	var shapeStr string
	for _, r := range s {
		if r == '(' || r == ')' || r == ',' {
			continue
		}

		shapeStr += string([]rune{r})
	}

	var shape []int64
	if len(shapeStr) == 0 {
		shape = make([]int64, 0)
	} else {
		size := strings.Split(shapeStr, ",")
		for _, v := range size {
			dim, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, err
			}
			shape = append(shape, int64(dim))
		}
	}

	return &Header{
		descr,
		fortranOrder,
		shape,
	}, nil
}

// ReadNpy reads a .npy file and returns the stored tensor.
func ReadNpy(filepath string) (*Tensor, error) {

	f, err := os.Open(filepath)
	if err != nil {
		return nil, err
	}

	r := bufio.NewReader(f)

	h, err := readHeader(r)
	if err != nil {
		return nil, err
	}

	hd := new(Header)

	header, err := hd.parse(h)
	if err != nil {
		return nil, err
	}
	if header.fortranOrder {
		err := fmt.Errorf("fortran order not supported.\n")
		return nil, err
	}

	// Read all the rest
	var data []byte
	data, err = ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}

	return OfDataSize(data, header.shape, header.descr)
}
