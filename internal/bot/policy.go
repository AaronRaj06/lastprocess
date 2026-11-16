package bot

import (
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/AaronRaj06/lastprocess/internal/world"
)

// Policy runs an RL policy exported to ONNX. It is a deliberately tiny
// interpreter: it reads the ONNX protobuf directly and supports only what an
// MLP needs (Gemm, MatMul, Add, Tanh, Relu, Flatten, Identity, as one chain).
// That keeps the server free of cgo and of a 30 MB runtime; see decisions D12.
type Policy struct{ layers []layer }

type layer struct {
	op      string
	w       []float32 // row-major [out][in]
	b       []float32
	in, out int
}

//go:embed policy.onnx
var embedded []byte

// EmbeddedPolicy is the trained policy compiled into the binary.
func EmbeddedPolicy() (*Policy, error) { return ParsePolicy(embedded) }

// LoadPolicy reads an ONNX file.
func LoadPolicy(path string) (*Policy, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePolicy(b)
}

// field is one decoded protobuf field: a varint/fixed value or a byte payload.
type field struct {
	num int
	val uint64
	buf []byte
}

// fields decodes one protobuf message level. Only the wire types ONNX uses are handled.
func fields(b []byte) ([]field, error) {
	var out []field
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			return nil, errors.New("onnx: bad field key")
		}
		b = b[n:]
		f := field{num: int(key >> 3)}
		switch key & 7 {
		case 0:
			if f.val, n = binary.Uvarint(b); n <= 0 {
				return nil, errors.New("onnx: bad varint")
			}
		case 1:
			if n = 8; len(b) < n {
				return nil, errors.New("onnx: truncated")
			}
			f.val = binary.LittleEndian.Uint64(b)
		case 5:
			if n = 4; len(b) < n {
				return nil, errors.New("onnx: truncated")
			}
			f.val = uint64(binary.LittleEndian.Uint32(b))
		case 2:
			l, m := binary.Uvarint(b)
			if m <= 0 || uint64(len(b)-m) < l {
				return nil, errors.New("onnx: truncated")
			}
			f.buf, n = b[m:m+int(l)], m+int(l)
		default:
			return nil, fmt.Errorf("onnx: unsupported wire type %d", key&7)
		}
		b = b[n:]
		out = append(out, f)
	}
	return out, nil
}

type tensor struct {
	dims []int
	data []float32
}

// ONNX field numbers used below: ModelProto.graph=7; GraphProto.node=1,
// initializer=5; NodeProto.input=1, output=2, op_type=4, attribute=5;
// TensorProto.dims=1, data_type=2, float_data=4, name=8, raw_data=9;
// AttributeProto.name=1, i=3.
func parseTensor(b []byte) (name string, t tensor, err error) {
	fs, err := fields(b)
	if err != nil {
		return "", t, err
	}
	for _, f := range fs {
		switch f.num {
		case 1:
			if f.buf == nil {
				t.dims = append(t.dims, int(f.val))
			}
			for p := f.buf; len(p) > 0; { // packed encoding
				v, n := binary.Uvarint(p)
				if n <= 0 {
					return "", t, errors.New("onnx: bad dims")
				}
				t.dims, p = append(t.dims, int(v)), p[n:]
			}
		case 2:
			if f.val != 1 {
				return "", t, fmt.Errorf("onnx: tensor type %d, want float32", f.val)
			}
		case 4, 9:
			if f.buf == nil {
				t.data = append(t.data, math.Float32frombits(uint32(f.val)))
			}
			for p := f.buf; len(p) >= 4; p = p[4:] {
				t.data = append(t.data, math.Float32frombits(binary.LittleEndian.Uint32(p)))
			}
		case 8:
			name = string(f.buf)
		}
	}
	n := 1
	for _, d := range t.dims {
		n *= d
	}
	if n != len(t.data) {
		return "", t, fmt.Errorf("onnx: tensor %q has %d values for dims %v", name, len(t.data), t.dims)
	}
	return name, t, nil
}

// ParsePolicy builds a Policy from ONNX bytes and checks it maps ObsLen inputs to NumActions outputs.
func ParsePolicy(b []byte) (*Policy, error) {
	model, err := fields(b)
	if err != nil {
		return nil, err
	}
	var graph []field
	for _, f := range model {
		if f.num == 7 {
			if graph, err = fields(f.buf); err != nil {
				return nil, err
			}
		}
	}
	tensors := map[string]tensor{}
	for _, f := range graph {
		if f.num == 5 {
			name, t, err := parseTensor(f.buf)
			if err != nil {
				return nil, err
			}
			tensors[name] = t
		}
	}
	p := &Policy{}
	width, prevOut := world.ObsLen, ""
	for _, f := range graph {
		if f.num != 1 {
			continue
		}
		node, err := fields(f.buf)
		if err != nil {
			return nil, err
		}
		var in []string
		var out, op string
		transB := false
		for _, nf := range node {
			switch nf.num {
			case 1:
				in = append(in, string(nf.buf))
			case 2:
				out = string(nf.buf)
			case 4:
				op = string(nf.buf)
			case 5:
				attr, err := fields(nf.buf)
				if err != nil {
					return nil, err
				}
				name, val := "", uint64(0)
				for _, af := range attr {
					switch af.num {
					case 1:
						name = string(af.buf)
					case 3:
						val = af.val
					}
				}
				transB = transB || (name == "transB" && val == 1)
			}
		}
		if len(in) == 0 || (prevOut != "" && in[0] != prevOut) {
			return nil, fmt.Errorf("onnx: node %s is not part of a simple chain", op)
		}
		prevOut = out
		l := layer{op: op, in: width, out: width}
		switch op {
		case "Tanh", "Relu":
		case "Flatten", "Identity":
			continue
		case "Add":
			if len(in) != 2 || len(tensors[in[1]].data) != width {
				return nil, errors.New("onnx: Add needs a constant bias of the layer width")
			}
			l.b = tensors[in[1]].data
		case "Gemm", "MatMul":
			if len(in) < 2 || len(tensors[in[1]].dims) != 2 {
				return nil, fmt.Errorf("onnx: %s needs a constant 2-D weight", op)
			}
			w := tensors[in[1]]
			if transB { // PyTorch Linear layout: [out][in]
				l.out, l.w = w.dims[0], w.data
				if w.dims[1] != width {
					return nil, fmt.Errorf("onnx: %s weight is %v, input is %d wide", op, w.dims, width)
				}
			} else { // [in][out]: transpose once at load
				if w.dims[0] != width {
					return nil, fmt.Errorf("onnx: %s weight is %v, input is %d wide", op, w.dims, width)
				}
				l.out, l.w = w.dims[1], make([]float32, len(w.data))
				for i := range w.dims[0] {
					for o := range w.dims[1] {
						l.w[o*width+i] = w.data[i*w.dims[1]+o]
					}
				}
			}
			if len(in) > 2 {
				if l.b = tensors[in[2]].data; len(l.b) != l.out {
					return nil, errors.New("onnx: Gemm bias has the wrong size")
				}
			}
		default:
			return nil, fmt.Errorf("onnx: unsupported operator %q", op)
		}
		width = l.out
		p.layers = append(p.layers, l)
	}
	if len(p.layers) == 0 || width != int(world.NumActions) {
		return nil, fmt.Errorf("onnx: policy outputs %d values, want %d", width, world.NumActions)
	}
	return p, nil
}

// Logits runs the network on x (len ObsLen) and returns the action scores.
func (p *Policy) Logits(x []float32) []float32 {
	for _, l := range p.layers {
		switch l.op {
		case "Tanh":
			for i, v := range x {
				x[i] = float32(math.Tanh(float64(v)))
			}
		case "Relu":
			for i, v := range x {
				x[i] = max(v, 0)
			}
		case "Add":
			for i := range x {
				x[i] += l.b[i]
			}
		default:
			y := make([]float32, l.out)
			for o := range y {
				// Four accumulators break the add dependency chain (about 3x
				// faster). The float32 conversions forbid fusing into an FMA,
				// so the result is identical on arm64 and amd64.
				row, i := l.w[o*l.in:(o+1)*l.in], 0
				var s0, s1, s2, s3 float32
				for ; i+4 <= len(row); i += 4 {
					s0 += float32(row[i] * x[i])
					s1 += float32(row[i+1] * x[i+1])
					s2 += float32(row[i+2] * x[i+2])
					s3 += float32(row[i+3] * x[i+3])
				}
				for ; i < len(row); i++ {
					s0 += float32(row[i] * x[i])
				}
				y[o] = (s0 + s1) + (s2 + s3)
				if l.b != nil {
					y[o] += l.b[o]
				}
			}
			x = y
		}
	}
	return x
}

// Controller returns a bot that plays the policy greedily (argmax, so it needs no RNG).
func (p *Policy) Controller() Controller {
	buf := make([]float32, world.ObsLen)
	return Func(func(o *world.Observation) world.Action {
		if !o.Alive {
			return world.Stay
		}
		o.Vector(buf)
		logits := p.Logits(buf)
		best := 0
		for i, v := range logits {
			if v > logits[best] {
				best = i
			}
		}
		return world.Action(best)
	})
}
