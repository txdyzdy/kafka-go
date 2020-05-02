package protocol

import (
	"fmt"
	"io"
	"io/ioutil"
	"reflect"
)

type discarder interface {
	Discard(int) (int, error)
}

type decoder struct {
	reader io.Reader
	remain int
	buffer [8]byte
	err    error
}

func (d *decoder) Read(b []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	if d.remain == 0 {
		return 0, io.EOF
	}
	if len(b) > d.remain {
		b = b[:d.remain]
	}
	n, err := d.reader.Read(b)
	d.remain -= n
	return n, err
}

func (d *decoder) decodeBool(v value) {
	v.setBool(d.readBool())
}

func (d *decoder) decodeInt8(v value) {
	v.setInt8(d.readInt8())
}

func (d *decoder) decodeInt16(v value) {
	v.setInt16(d.readInt16())
}

func (d *decoder) decodeInt32(v value) {
	v.setInt32(d.readInt32())
}

func (d *decoder) decodeInt64(v value) {
	v.setInt64(d.readInt64())
}

func (d *decoder) decodeString(v value) {
	v.setString(d.readString())
}

func (d *decoder) decodeBytes(v value) {
	v.setBytes(d.readBytes())
}

func (d *decoder) decodeArray(v value, elemType reflect.Type, decodeElem decodeFunc) {
	if n := d.readInt32(); n < 0 {
		v.setArray(array{})
	} else {
		a := makeArray(elemType, int(n))
		for i := 0; i < int(n) && d.remain > 0; i++ {
			decodeElem(d, a.index(i))
		}
		v.setArray(a)
	}
}

func (d *decoder) discardAll() {
	d.discard(d.remain)
}

func (d *decoder) discard(n int) {
	if n > d.remain {
		n = d.remain
	}
	var err error
	if r, _ := d.reader.(discarder); r != nil {
		n, err = r.Discard(n)
		d.remain -= n
	} else {
		_, err = io.Copy(ioutil.Discard, d)
	}
	d.setError(err)
}

func (d *decoder) read(n int) []byte {
	b := make([]byte, n)
	n, err := io.ReadFull(d, b)
	b = b[:n]
	d.setError(err)
	return b
}

func (d *decoder) writeTo(w io.Writer, n int) {
	if int(n) > d.remain {
		d.setError(io.ErrUnexpectedEOF)
	} else {
		remain := d.remain
		if n < remain {
			d.remain = n
		}
		c, err := io.Copy(w, d)
		if c < int64(n) && err == nil {
			err = io.ErrUnexpectedEOF
		}
		d.remain = remain - int(n)
		d.setError(err)
	}
}

func (d *decoder) setError(err error) {
	if d.err == nil && err != nil {
		d.err = err
		d.discardAll()
	}
}

func (d *decoder) readFull(b []byte) bool {
	n, err := io.ReadFull(d, b)
	d.setError(err)
	return n == len(b)
}

func (d *decoder) readByte() byte {
	if d.readFull(d.buffer[:1]) {
		return d.buffer[0]
	}
	return 0
}

func (d *decoder) readBool() bool {
	return d.readByte() != 0
}

func (d *decoder) readInt8() int8 {
	return int8(d.readByte())
}

func (d *decoder) readInt16() int16 {
	if d.readFull(d.buffer[:2]) {
		return readInt16(d.buffer[:2])
	}
	return 0
}

func (d *decoder) readInt32() int32 {
	if d.readFull(d.buffer[:4]) {
		return readInt32(d.buffer[:4])
	}
	return 0
}

func (d *decoder) readInt64() int64 {
	if d.readFull(d.buffer[:8]) {
		return readInt64(d.buffer[:8])
	}
	return 0
}

func (d *decoder) readString() string {
	if n := d.readInt16(); n < 0 {
		return ""
	} else {
		return bytesToString(d.read(int(n)))
	}
}

func (d *decoder) readCompactString() string {
	if n := d.readVarInt(); n < 0 {
		return ""
	} else {
		return bytesToString(d.read(int(n)))
	}
}

func (d *decoder) readBytes() []byte {
	if n := d.readInt32(); n < 0 {
		return nil
	} else {
		return d.read(int(n))
	}
}

func (d *decoder) readBytesTo(w io.Writer) bool {
	if n := d.readInt32(); n < 0 {
		return false
	} else {
		d.writeTo(w, int(n))
		return d.err == nil
	}
}

func (d *decoder) readCompactBytes() []byte {
	if n := d.readVarInt(); n < 0 {
		return nil
	} else {
		return d.read(int(n))
	}
}

func (d *decoder) readCompactBytesTo(w io.Writer) bool {
	if n := d.readVarInt(); n < 0 {
		return false
	} else {
		d.writeTo(w, int(n))
		return d.err == nil
	}
}

func (d *decoder) readVarInt() int64 {
	n := 11 // varints are at most 11 bytes

	if n > d.remain {
		n = d.remain
	}

	x := uint64(0)
	s := uint(0)

	for n > 0 {
		b := d.readByte()

		if (b & 0x80) == 0 {
			x |= uint64(b) << s
			return int64(x>>1) ^ -(int64(x) & 1)
		}

		x |= uint64(b&0x7f) << s
		s += 7
		n--
	}

	d.setError(fmt.Errorf("cannot decode varint from input stream"))
	return 0
}

type decodeFunc func(*decoder, value)

var (
	readerFrom = reflect.TypeOf((*io.ReaderFrom)(nil)).Elem()
)

func decodeFuncOf(typ reflect.Type, version int16, tag structTag) decodeFunc {
	if reflect.PtrTo(typ).Implements(readerFrom) {
		return readerDecodeFuncOf(typ)
	}
	switch typ.Kind() {
	case reflect.Bool:
		return (*decoder).decodeBool
	case reflect.Int8:
		return (*decoder).decodeInt8
	case reflect.Int16:
		return (*decoder).decodeInt16
	case reflect.Int32:
		return (*decoder).decodeInt32
	case reflect.Int64:
		return (*decoder).decodeInt64
	case reflect.String:
		return stringDecodeFuncOf(tag)
	case reflect.Struct:
		return structDecodeFuncOf(typ, version)
	case reflect.Slice:
		if typ.Elem().Kind() == reflect.Uint8 { // []byte
			return bytesDecodeFuncOf(tag)
		}
		return arrayDecodeFuncOf(typ, version, tag)
	default:
		panic("unsupported type: " + typ.String())
	}
}

func stringDecodeFuncOf(tag structTag) decodeFunc {
	return (*decoder).decodeString
}

func bytesDecodeFuncOf(tag structTag) decodeFunc {
	return (*decoder).decodeBytes
}

func structDecodeFuncOf(typ reflect.Type, version int16) decodeFunc {
	type field struct {
		decode decodeFunc
		index  index
	}

	var fields []field
	forEachStructField(typ, func(typ reflect.Type, index index, tag string) {
		forEachStructTag(tag, func(tag structTag) bool {
			if tag.MinVersion <= version && version <= tag.MaxVersion {
				fields = append(fields, field{
					decode: decodeFuncOf(typ, version, tag),
					index:  index,
				})
				return false
			}
			return true
		})
	})

	return func(d *decoder, v value) {
		for i := range fields {
			f := &fields[i]
			f.decode(d, v.fieldByIndex(f.index))
		}
	}
}

func arrayDecodeFuncOf(typ reflect.Type, version int16, tag structTag) decodeFunc {
	elemType := typ.Elem()
	elemFunc := decodeFuncOf(elemType, version, tag)
	return func(d *decoder, v value) { d.decodeArray(v, elemType, elemFunc) }
}

func readerDecodeFuncOf(typ reflect.Type) decodeFunc {
	typ = reflect.PtrTo(typ)
	return func(d *decoder, v value) {
		if d.err == nil {
			_, d.err = v.iface(typ).(io.ReaderFrom).ReadFrom(d.reader)
		}
	}
}
