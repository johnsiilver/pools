package diffsize

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"testing"
	"unicode/utf8"
)

var pool *Pool[*[]byte]

func init() {
	var err error
	pool, err = New[*[]byte](
		Sizes{
			{Size: 16, ConstBuff: 10, SyncPool: true},
			{Size: 32, ConstBuff: 10, SyncPool: true},
			{Size: 64, ConstBuff: 10, SyncPool: true},
			{Size: 128, ConstBuff: 10, SyncPool: true},
			{Size: 256, ConstBuff: 10, SyncPool: true},
			{Size: 512, ConstBuff: 10, SyncPool: true},
			{Size: 1024, ConstBuff: 10, SyncPool: true},
		},
	)
	if err != nil {
		panic(err)
	}
}

const N = 10000       // make this bigger for a larger (and slower) test
var testString string // test data for write tests
var testBytes []byte  // test data; same as testString but as a slice.

type negativeReader struct{}

func (r *negativeReader) Read([]byte) (int, error) { return -1, nil }

func init() {
	testBytes = make([]byte, N)
	for i := 0; i < N; i++ {
		testBytes[i] = 'a' + byte(i%26)
	}
	testString = string(testBytes)
}

// Verify that contents of buf match the string s.
func check(t *testing.T, testname string, buf *PoolBuffer, s string) {
	bytes := buf.Bytes()
	str := buf.String()
	if buf.Len() != len(bytes) {
		t.Errorf("%s: buf.Len() == %d, len(buf.Bytes()) == %d", testname, buf.Len(), len(bytes))
	}

	if buf.Len() != len(str) {
		t.Errorf("%s: buf.Len() == %d, len(buf.String()) == %d", testname, buf.Len(), len(str))
	}

	if buf.Len() != len(s) {
		t.Errorf("%s: buf.Len() == %d, len(s) == %d", testname, buf.Len(), len(s))
	}

	if string(bytes) != s {
		t.Errorf("%s: string(buf.Bytes()) == %q, s == %q", testname, string(bytes), s)
	}
}

// Fill buf through n writes of string fus.
// The initial contents of buf corresponds to the string s;
// the result is the final contents of buf returned as a string.
func fillString(t *testing.T, testname string, buf *PoolBuffer, s string, n int, fus string) string {
	check(t, testname+" (fill 1)", buf, s)
	for ; n > 0; n-- {
		m, err := buf.WriteString(fus)
		if m != len(fus) {
			t.Errorf(testname+" (fill 2): m == %d, expected %d", m, len(fus))
		}
		if err != nil {
			t.Errorf(testname+" (fill 3): err should always be nil, found err == %s", err)
		}
		s += fus
		check(t, testname+" (fill 4)", buf, s)
	}
	return s
}

// Fill buf through n writes of byte slice fub.
// The initial contents of buf corresponds to the string s;
// the result is the final contents of buf returned as a string.
func fillBytes(t *testing.T, testname string, buf *PoolBuffer, s string, n int, fub []byte) string {
	check(t, testname+" (fill 1)", buf, s)
	for ; n > 0; n-- {
		m, err := buf.Write(fub)
		if m != len(fub) {
			t.Errorf(testname+" (fill 2): m == %d, expected %d", m, len(fub))
		}
		if err != nil {
			t.Errorf(testname+" (fill 3): err should always be nil, found err == %s", err)
		}
		s += string(fub)
		check(t, testname+" (fill 4)", buf, s)
	}
	return s
}

// Empty buf through repeated reads into fub.
// The initial contents of buf corresponds to the string s.
func empty(t *testing.T, testname string, buf *PoolBuffer, s string, fub []byte) {
	check(t, testname+" (empty 1)", buf, s)

	for {
		n, err := buf.Read(fub)
		if n == 0 {
			break
		}
		if err != nil {
			t.Errorf(testname+" (empty 2): err should always be nil, found err == %s", err)
		}
		s = s[n:]
		check(t, testname+" (empty 3)", buf, s)
	}

	check(t, testname+" (empty 4)", buf, "")
}

func TestBasicOperations(t *testing.T) {
	buf := NewPoolBuffer(pool, 0)
	defer buf.Close()

	for i := 0; i < 5; i++ {
		check(t, "TestBasicOperations (1)", buf, "")

		buf.Reset()
		check(t, "TestBasicOperations (2)", buf, "")

		buf.Truncate(0)
		check(t, "TestBasicOperations (3)", buf, "")

		n, err := buf.Write(testBytes[0:1])
		if want := 1; err != nil || n != want {
			t.Errorf("Write: got (%d, %v), want (%d, %v)", n, err, want, nil)
		}
		check(t, "TestBasicOperations (4)", buf, "a")

		buf.WriteByte(testString[1])
		check(t, "TestBasicOperations (5)", buf, "ab")

		n, err = buf.Write(testBytes[2:26])
		if want := 24; err != nil || n != want {
			t.Errorf("Write: got (%d, %v), want (%d, %v)", n, err, want, nil)
		}
		check(t, "TestBasicOperations (6)", buf, testString[0:26])

		buf.Truncate(26)
		check(t, "TestBasicOperations (7)", buf, testString[0:26])

		buf.Truncate(20)
		check(t, "TestBasicOperations (8)", buf, testString[0:20])

		empty(t, "TestBasicOperations (9)", buf, testString[0:20], make([]byte, 5))
		empty(t, "TestBasicOperations (10)", buf, "", make([]byte, 100))

		buf.WriteByte(testString[1])
		c, err := buf.ReadByte()
		if want := testString[1]; err != nil || c != want {
			t.Errorf("ReadByte: got (%q, %v), want (%q, %v)", c, err, want, nil)
		}
		c, err = buf.ReadByte()
		if err != io.EOF {
			t.Errorf("ReadByte: got (%q, %v), want (%q, %v)", c, err, byte(0), io.EOF)
		}
	}
}

func TestLargeStringWrites(t *testing.T) {
	buf := NewPoolBuffer(pool, 0)
	defer buf.Close()

	limit := 30
	if testing.Short() {
		limit = 9
	}
	for i := 3; i < limit; i += 3 {
		s := fillString(t, "TestLargeWrites (1)", buf, "", 5, testString)
		empty(t, "TestLargeStringWrites (2)", buf, s, make([]byte, len(testString)/i))
	}
	check(t, "TestLargeStringWrites (3)", buf, "")
}

func TestLargeByteWrites(t *testing.T) {
	buf := NewPoolBuffer(pool, 0)
	defer buf.Close()

	limit := 30
	if testing.Short() {
		limit = 9
	}
	for i := 3; i < limit; i += 3 {
		s := fillBytes(t, "TestLargeWrites (1)", buf, "", 5, testBytes)
		empty(t, "TestLargeByteWrites (2)", buf, s, make([]byte, len(testString)/i))
	}
	check(t, "TestLargeByteWrites (3)", buf, "")
}

func TestLargeStringReads(t *testing.T) {
	buf := NewPoolBuffer(pool, 0)
	defer buf.Close()

	for i := 3; i < 30; i += 3 {
		s := fillString(t, "TestLargeReads (1)", buf, "", 5, testString[0:len(testString)/i])
		empty(t, "TestLargeReads (2)", buf, s, make([]byte, len(testString)))
	}
	check(t, "TestLargeStringReads (3)", buf, "")
}

func TestLargeByteReads(t *testing.T) {
	buf := NewPoolBuffer(pool, 0)
	defer buf.Close()

	for i := 3; i < 30; i += 3 {
		s := fillBytes(t, "TestLargeReads (1)", buf, "", 5, testBytes[0:len(testBytes)/i])
		empty(t, "TestLargeReads (2)", buf, s, make([]byte, len(testString)))
	}
	check(t, "TestLargeByteReads (3)", buf, "")
}

func TestMixedReadsAndWrites(t *testing.T) {
	buf := NewPoolBuffer(pool, 0)
	defer buf.Close()

	s := ""
	for i := 0; i < 50; i++ {
		wlen := rand.Intn(len(testString))
		if i%2 == 0 {
			s = fillString(t, "TestMixedReadsAndWrites (1)", buf, s, 1, testString[0:wlen])
		} else {
			s = fillBytes(t, "TestMixedReadsAndWrites (1)", buf, s, 1, testBytes[0:wlen])
		}

		rlen := rand.Intn(len(testString))
		fub := make([]byte, rlen)
		n, _ := buf.Read(fub)
		s = s[n:]
	}
	empty(t, "TestMixedReadsAndWrites (2)", buf, s, make([]byte, buf.Len()))
}

func TestCapWithPreallocatedSlice(t *testing.T) {
	buf := NewPoolBuffer(pool, 10)
	defer buf.Close()
	n := buf.Cap()
	if n != 16 {
		t.Errorf("expected 16, got %d", n)
	}
}

func TestCapWithSliceAndWrittenData(t *testing.T) {
	buf := NewPoolBuffer(pool, 10)
	defer buf.Close()
	buf.Write([]byte("test"))
	n := buf.Cap()
	if n != 16 {
		t.Errorf("expected 16, got %d", n)
	}
}

func TestNil(t *testing.T) {
	// This is one example where having a nil PoolBuffer is legal.
	var buf *PoolBuffer
	if buf.String() != "<nil>" {
		t.Errorf("expected <nil>; got %q", buf.String())
	}
}

func TestReadFrom(t *testing.T) {
	buf := NewPoolBuffer(pool, 0)
	defer buf.Close()

	for i := 3; i < 30; i += 3 {
		s := fillBytes(t, "TestReadFrom (1)", buf, "", 5, testBytes[0:len(testBytes)/i])
		b := NewPoolBuffer(pool, 0)
		b.ReadFrom(buf)
		empty(t, "TestReadFrom (2)", b, s, make([]byte, len(testString)))
		b.Close()
	}
}

type panicReader struct{ panic bool }

func (r panicReader) Read(p []byte) (int, error) {
	if r.panic {
		panic(nil)
	}
	return 0, io.EOF
}

// Make sure that an empty PoolBuffer remains empty when
// it is "grown" before a Read that panics
func TestReadFromPanicReader(t *testing.T) {
	buf := NewPoolBuffer(pool, 0)
	defer buf.Close()

	// First verify non-panic behaviour
	i, err := buf.ReadFrom(panicReader{})
	if err != nil {
		t.Fatal(err)
	}
	if i != 0 {
		t.Fatalf("unexpected return from bytes.ReadFrom (1): got: %d, want %d", i, 0)
	}
	check(t, "TestReadFromPanicReader (1)", buf, "")

	// Confirm that when Reader panics, the empty buffer remains empty
	buf2 := NewPoolBuffer(pool, 0)
	defer buf2.Close()
	defer func() {
		recover()
		check(t, "TestReadFromPanicReader (2)", buf2, "")
	}()
	buf2.ReadFrom(panicReader{panic: true})
}

func TestReadFromNegativeReader(t *testing.T) {
	b := NewPoolBuffer(pool, 0)
	defer b.Close()

	defer func() {
		switch err := recover().(type) {
		case nil:
			t.Fatal("bytes.PoolBuffer.ReadFrom didn't panic")
		case error:
			// this is the error string of errNegativeRead
			wantError := "bytes.PoolBuffer: reader returned negative count from Read"
			if err.Error() != wantError {
				t.Fatalf("recovered panic: got %v, want %v", err.Error(), wantError)
			}
		default:
			t.Fatalf("unexpected panic value: %#v", err)
		}
	}()

	b.ReadFrom(new(negativeReader))
}

func TestWriteTo(t *testing.T) {
	buf := NewPoolBuffer(pool, 0)
	defer buf.Close()

	for i := 3; i < 30; i += 3 {
		s := fillBytes(t, "TestWriteTo (1)", buf, "", 5, testBytes[0:len(testBytes)/i])
		b := NewPoolBuffer(pool, 0)
		buf.WriteTo(b)
		empty(t, "TestWriteTo (2)", b, s, make([]byte, len(testString)))
		b.Close()
	}
}

func TestRuneIO(t *testing.T) {
	buf := NewPoolBuffer(pool, 0)
	defer buf.Close()

	const NRune = 1000
	// Built a test slice while we write the data
	b := make([]byte, utf8.UTFMax*NRune)
	n := 0
	for r := rune(0); r < NRune; r++ {
		size := utf8.EncodeRune(b[n:], r)
		nbytes, err := buf.WriteRune(r)
		if err != nil {
			t.Fatalf("WriteRune(%U) error: %s", r, err)
		}
		if nbytes != size {
			t.Fatalf("WriteRune(%U) expected %d, got %d", r, size, nbytes)
		}
		n += size
	}
	b = b[0:n]

	// Check the resulting bytes
	if !bytes.Equal(buf.Bytes(), b) {
		t.Fatalf("incorrect result from WriteRune: %q not %q", buf.Bytes(), b)
	}

	p := make([]byte, utf8.UTFMax)
	// Read it back with ReadRune
	for r := rune(0); r < NRune; r++ {
		size := utf8.EncodeRune(p, r)
		nr, nbytes, err := buf.ReadRune()
		if nr != r || nbytes != size || err != nil {
			t.Fatalf("ReadRune(%U) got %U,%d not %U,%d (err=%s)", r, nr, nbytes, r, size, err)
		}
	}

	// Check that UnreadRune works
	buf.Reset()

	// check at EOF
	if err := buf.UnreadRune(); err == nil {
		t.Fatal("UnreadRune at EOF: got no error")
	}
	if _, _, err := buf.ReadRune(); err == nil {
		t.Fatal("ReadRune at EOF: got no error")
	}
	if err := buf.UnreadRune(); err == nil {
		t.Fatal("UnreadRune after ReadRune at EOF: got no error")
	}

	// check not at EOF
	buf.Write(b)
	for r := rune(0); r < NRune; r++ {
		r1, size, _ := buf.ReadRune()
		if err := buf.UnreadRune(); err != nil {
			t.Fatalf("UnreadRune(%U) got error %q", r, err)
		}
		r2, nbytes, err := buf.ReadRune()
		if r1 != r2 || r1 != r || nbytes != size || err != nil {
			t.Fatalf("ReadRune(%U) after UnreadRune got %U,%d not %U,%d (err=%s)", r, r2, nbytes, r, size, err)
		}
	}
}

func TestWriteInvalidRune(t *testing.T) {
	// Invalid runes, including negative ones, should be written as
	// utf8.RuneError.
	for _, r := range []rune{-1, utf8.MaxRune + 1} {
		buf := NewPoolBuffer(pool, 0)
		buf.WriteRune(r)
		check(t, fmt.Sprintf("TestWriteInvalidRune (%d)", r), buf, "\uFFFD")
		buf.Close()
	}
}

var readBytesTests = []struct {
	buffer   string
	delim    byte
	expected []string
	err      error
}{
	{"", 0, []string{""}, io.EOF},
	{"a\x00", 0, []string{"a\x00"}, nil},
	{"abbbaaaba", 'b', []string{"ab", "b", "b", "aaab"}, nil},
	{"hello\x01world", 1, []string{"hello\x01"}, nil},
	{"foo\nbar", 0, []string{"foo\nbar"}, io.EOF},
	{"alpha\nbeta\ngamma\n", '\n', []string{"alpha\n", "beta\n", "gamma\n"}, nil},
	{"alpha\nbeta\ngamma", '\n', []string{"alpha\n", "beta\n", "gamma"}, io.EOF},
}

func TestReadBytes(t *testing.T) {
	for _, test := range readBytesTests {
		func() {
			buf := NewPoolBuffer(pool, len(test.buffer))
			defer buf.Close()
			buf.WriteString(test.buffer)
			var err error
			for _, expected := range test.expected {
				var bytes []byte
				bytes, err = buf.ReadBytes(test.delim)
				if string(bytes) != expected {
					t.Errorf("expected %q, got %q", expected, bytes)
				}
				if err != nil {
					break
				}
			}
			if err != test.err {
				t.Errorf("expected error %v, got %v", test.err, err)
			}
		}()
	}
}

func TestReadString(t *testing.T) {
	for _, test := range readBytesTests {
		func() {
			buf := NewPoolBuffer(pool, len(test.buffer))
			defer buf.Close()
			buf.WriteString(test.buffer)
			var err error
			for _, expected := range test.expected {
				var s string
				s, err = buf.ReadString(test.delim)
				if s != expected {
					t.Errorf("expected %q, got %q", expected, s)
				}
				if err != nil {
					break
				}
			}
			if err != test.err {
				t.Errorf("expected error %v, got %v", test.err, err)
			}
		}()
	}
}

func BenchmarkReadString(b *testing.B) {
	const n = 32 << 10

	data := make([]byte, n)
	data[n-1] = 'x'
	b.SetBytes(int64(n))
	for i := 0; i < b.N; i++ {
		buf := NewPoolBuffer(pool, len(data))
		buf.Write(data)
		_, err := buf.ReadString('x')
		if err != nil {
			b.Fatal(err)
		}
		buf.Close()
	}
}

func TestGrow(t *testing.T) {
	x := []byte{'x'}
	y := []byte{'y'}
	tmp := make([]byte, 72)
	for _, growLen := range []int{0, 100, 1000, 10000, 100000} {
		for _, startLen := range []int{0, 100, 1000, 10000, 100000} {
			xBytes := bytes.Repeat(x, startLen)

			func() {
				buf := NewPoolBuffer(pool, len(xBytes))
				defer buf.Close()
				buf.Write(xBytes)

				// If we read, this affects buf.off, which is good to test.
				readBytes, _ := buf.Read(tmp)
				yBytes := bytes.Repeat(y, growLen)
				allocs := testing.AllocsPerRun(100, func() {
					buf.Grow(growLen)
					buf.Write(yBytes)
				})
				// Check no allocation occurs in write, as long as we're single-threaded.
				if allocs != 0 {
					t.Errorf("allocation occurred during write")
				}
				// Check that buffer has correct data.
				if !bytes.Equal(buf.Bytes()[0:startLen-readBytes], xBytes[readBytes:]) {
					t.Errorf("bad initial data at %d %d", startLen, growLen)
				}
				if !bytes.Equal(buf.Bytes()[startLen-readBytes:startLen-readBytes+growLen], yBytes) {
					t.Errorf("bad written data at %d %d", startLen, growLen)
				}
			}()
		}
	}
}

func TestGrowOverflow(t *testing.T) {
	defer func() {
		if err := recover(); err != ErrTooLarge {
			t.Errorf("after too-large Grow, recover() = %v; want %v", err, ErrTooLarge)
		}
	}()

	buf := NewPoolBuffer(pool, 16)
	const maxInt = int(^uint(0) >> 1)
	buf.Grow(maxInt)
	buf.Close()
}

// Was a bug: used to give EOF reading empty slice at EOF.
func TestReadEmptyAtEOF(t *testing.T) {
	b := NewPoolBuffer(pool, 0)
	defer b.Close()
	slice := make([]byte, 0)
	n, err := b.Read(slice)
	if err != nil {
		t.Errorf("read error: %v", err)
	}
	if n != 0 {
		t.Errorf("wrong count; got %d want 0", n)
	}
}

func TestUnreadByte(t *testing.T) {
	b := NewPoolBuffer(pool, 0)
	defer b.Close()
	// check at EOF
	if err := b.UnreadByte(); err == nil {
		t.Fatal("UnreadByte at EOF: got no error")
	}
	if _, err := b.ReadByte(); err == nil {
		t.Fatal("ReadByte at EOF: got no error")
	}
	if err := b.UnreadByte(); err == nil {
		t.Fatal("UnreadByte after ReadByte at EOF: got no error")
	}

	// check not at EOF
	b.WriteString("abcdefghijklmnopqrstuvwxyz")

	// after unsuccessful read
	if n, err := b.Read(nil); n != 0 || err != nil {
		t.Fatalf("Read(nil) = %d,%v; want 0,nil", n, err)
	}
	if err := b.UnreadByte(); err == nil {
		t.Fatal("UnreadByte after Read(nil): got no error")
	}

	// after successful read
	if _, err := b.ReadBytes('m'); err != nil {
		t.Fatalf("ReadBytes: %v", err)
	}
	if err := b.UnreadByte(); err != nil {
		t.Fatalf("UnreadByte: %v", err)
	}
	c, err := b.ReadByte()
	if err != nil {
		t.Fatalf("ReadByte: %v", err)
	}
	if c != 'm' {
		t.Errorf("ReadByte = %q; want %q", c, 'm')
	}
}

// Tests that we occasionally compact. Issue 5154.
func TestPoolBufferGrowth(t *testing.T) {
	b := NewPoolBuffer(pool, 0)
	defer b.Close()

	buf := make([]byte, 1024)
	b.Write(buf[0:1])
	var cap0 int
	for i := 0; i < 5<<10; i++ {
		b.Write(buf)
		b.Read(buf)
		if i == 0 {
			cap0 = b.Cap()
		}
	}
	cap1 := b.Cap()
	// (*PoolBuffer).grow allows for 2x capacity slop before sliding,
	// so set our error threshold at 3x.
	if cap1 > cap0*3 {
		t.Errorf("buffer cap = %d; too big (grew from %d)", cap1, cap0)
	}
}

func BenchmarkWriteByte(b *testing.B) {
	b.ResetTimer()
	const n = 4 << 10
	b.SetBytes(n)
	buf := NewPoolBuffer(pool, n)
	defer buf.Close()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		for i := 0; i < n; i++ {
			buf.WriteByte('x')
		}
	}
}

func BenchmarkWriteRune(b *testing.B) {
	const n = 4 << 10
	const r = '☺'
	b.SetBytes(int64(n * utf8.RuneLen(r)))

	buf := NewPoolBuffer(pool, n*utf8.UTFMax)
	defer buf.Close()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		for i := 0; i < n; i++ {
			buf.WriteRune(r)
		}
	}
}

// From Issue 5154.
func BenchmarkPoolBufferNotEmptyWriteRead(b *testing.B) {
	buf := make([]byte, 1024)
	for i := 0; i < b.N; i++ {
		b := NewPoolBuffer(pool, 1024)
		b.Write(buf[0:1])
		for i := 0; i < 5<<10; i++ {
			b.Write(buf)
			b.Read(buf)
		}
		b.Close()
	}
}

// Check that we don't compact too often. From Issue 5154.
func BenchmarkPoolBufferFullSmallReads(b *testing.B) {
	buf := make([]byte, 1024)
	for i := 0; i < b.N; i++ {
		b := NewPoolBuffer(pool, 1024)
		b.Write(buf)
		for b.Len()+20 < b.Cap() {
			b.Write(buf[:10])
		}
		for i := 0; i < 5<<10; i++ {
			b.Read(buf[:1])
			b.Write(buf[:1])
		}
		b.Close()
	}
}

func BenchmarkPoolBufferWriteBlock(b *testing.B) {
	block := make([]byte, 1024)
	for _, n := range []int{1 << 12, 1 << 16, 1 << 20} {
		b.Run(fmt.Sprintf("N%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				bb := NewPoolBuffer(pool, 0)
				for bb.Len() < n {
					bb.Write(block)
				}
				bb.Close()
			}
		})
	}
}
