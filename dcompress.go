// dcompress.go  (c) 2013 David Rook All rights reserved.

package dcompress

import (
	"bytes"
	"errors"
	"io"
)

const (
	hSize     = 69001 // hash table size => 95% occupancy
	bitMask   = 0x1f
	blockMode = 0x80
	nBits     = uint8(16)
	//BUFSIZ     = 8192
	iBufSize = 8192
	oBufSize = iBufSize

	InitBits = uint8(9)
)

var (
	ErrBadMagic     = errors.New("dcompress: bad magic number")
	ErrCorruptInput = errors.New("dcompress: corrupt input")
	ErrMaxBitsExcd  = errors.New("dcompress: maxbits exceeded")
	ErrOther        = errors.New("dcompress: other error")
)

// NOTES:
// Z is a compression technique, not an archiver.
// NewReader will create a local copy of the uncompressed data.
// This will set upper limit on the size of an individual readable file.
// Memory use expected to be < (CompressedFileSize * 10) for non-pathological cases.
// Unpacks 23 MB in less than one second on 4 Ghz AMD 64 (8120^OC).
//
// Go code is based on literal translation of compress42.c (ie it's not idiomatic
// nor is it pretty) See doc.go for credits to the original writer(s)
// Kludge is to fix a problem with first character being written to output buffer as zero always.
// We save first output character and then patch it into outbuf at end.  Not sure why this happens.
// Other than the kludge it's a very literal translation of compress42.c
//
// NewReader takes compressed data from input source r and returns a reader for the uncompressed version
func NewReader(r io.Reader) (io.ReadSeeker, error) {
	var (
		FIRST      = int64(257)
		CLEAR      = int64(256)
		maxBits    = nBits
		blockMode  = blockMode
		stackNdx   int
		code       int64
		finchar    int
		oldcode    int64
		incode     int64
		inbits     int
		posbits    int
		outpos     int
		insize     int
		bitmask    int
		free_ent   int64
		maxcode    int64 = (1 << nBits) - 1
		maxmaxcode int64 = 1 << nBits
		n_bits     uint
		rsize      int
		bytes_in   int
		err        error
		firstChar  byte
		codesRead  int
		outBuf     []byte // this is what we return
	)

	var (
		//g_cBytes    []byte // compressed bytes
		g_inbuf    []byte // unpacking temp area
		g_outbuf   []byte // output staging area
		MagicBytes = []byte{0x1f, 0x9d}

		// hashTable - [hSize] unsigned long  in original, but used with byte level access
		// using *--stackp as one example  go doesn't have pointer arith so we adapt
		g_htab    [hSize * 8]byte
		g_codetab [hSize]uint16 // codeTable must be uint16
	)

	g_inbuf = make([]byte, iBufSize+64)
	g_outbuf = make([]byte, oBufSize+2048)
	outBuf = make([]byte, 0, 10000)

	rsize, err = r.Read(g_inbuf[0:iBufSize])
	if err != nil {
		return nil, err
	}
	insize += rsize

	if (g_inbuf[0] != MagicBytes[0]) || (g_inbuf[1] != MagicBytes[1]) {
		return nil, ErrBadMagic
	}

	maxBits = g_inbuf[2] & bitMask
	blockMode = int(g_inbuf[2]) & blockMode
	maxmaxcode = 1 << maxBits

	if maxBits > nBits {
		return nil, ErrMaxBitsExcd
	}

	// --- line 1650 in compress42.c ---
	bytes_in = insize
	n_bits = uint(InitBits)
	maxcode = (1 << n_bits) - 1
	bitmask = (1 << n_bits) - 1
	oldcode = -1
	finchar = 0
	outpos = 0
	posbits = 3 << 3

	if blockMode != 0 {
		free_ent = FIRST
	} else {
		free_ent = 256
	}

	// clear_tab_prefixof() =>  memset(codetab,0,256)  not req in go

	for code = 255; code >= 0; code-- {
		g_htab[code] = byte(code)
	}

	for {
	resetbuf:
		{
			o, e := 0, 0

			o = posbits >> 3
			if o <= insize {
				e = insize - o
			} else {
				e = 0
			}

			for i := 0; i < e; i++ {
				g_inbuf[i] = g_inbuf[i+o]
			}

			insize = e
			posbits = 0
		}

		// R E A D
		if insize < (len(g_inbuf) - iBufSize) {
			rsize, err = r.Read(g_inbuf[insize : insize+iBufSize])
			if err != nil {
				if err == io.EOF {
					// not an error
				} else {
					return nil, ErrCorruptInput
				}
			}
			insize += rsize
		}

		//	inbits = ((rsize > 0) ? (insize - insize%n_bits)<<3 : (insize<<3)-(n_bits-1));
		if rsize > 0 {
			inbits = (insize - (insize % int(n_bits))) << 3
		} else {
			inbits = (insize << 3) - (int(n_bits - 1))
		}

		for { // while inbits > posbits
			if inbits <= posbits {
				break
			}

			if free_ent > maxcode {
				posbits = (posbits - 1) + ((int(n_bits) << 3) - (posbits-1+(int(n_bits)<<3))%(int(n_bits)<<3))

				n_bits++
				if n_bits == uint(maxBits) {
					maxcode = maxmaxcode
				} else {
					maxcode = (1 << n_bits) - 1
				}
				bitmask = (1 << n_bits) - 1
				goto resetbuf
			}

			//input(inbuf,posbits,code,n_bits,bitmask);
			nBufNdx := posbits >> 3

			p1 := uint(g_inbuf[nBufNdx])
			p2 := uint(g_inbuf[nBufNdx+1]) << 8
			p3 := uint(g_inbuf[nBufNdx+2]) << 16 // bad index on larger files
			t1 := p1 | p2 | p3
			t2 := t1 >> uint(posbits&0x7)
			posbits += int(n_bits)

			code = int64(int(t2) & bitmask)
			codesRead++

			// BUG(mdr): <kludge alert>
			if firstChar == 0 {
				firstChar = byte(code)
			}
			// </kludge alert>

			if oldcode == -1 {
				if code >= 256 {
					return nil, ErrOther
				}
				oldcode = code
				finchar = int(oldcode)

				g_outbuf = append(g_outbuf, byte(finchar))
				outpos++
				continue
			}

			if (code == CLEAR) && (blockMode != 0) {
				// clear_tab_prefixof();#	define	clear_tab_prefixof()	memset(codetab, 0, 256);
				for i := 0; i < 256; i++ {
					g_codetab[i] = 0
				}
				free_ent = FIRST - 1
				posbits = (posbits - 1) + ((int(n_bits) << 3) - (posbits-1+(int(n_bits)<<3))%(int(n_bits)<<3))
				n_bits = uint(InitBits)
				maxcode = (1 << n_bits) - 1
				bitmask = (1 << n_bits) - 1
				goto resetbuf
			}

			incode = code

			//		stackp = de_stack
			//#	define	de_stack				((char_type *)&(htab[hSize-1]))
			stackNdx = hSize*8 - 1 // index of last element of htab

			if code >= free_ent { // BUG(mdr): original text ? Special case for KwKwK string
				if code > free_ent { // core dump no real help so dont print details
					return nil, ErrCorruptInput
				}

				/* #define	htabof(i)				htab[i]
				#	define	codetabof(i)			codetab[i]
				#	define	tab_prefixof(i)			codetabof(i)
				#	define	tab_suffixof(i)			((char_type *)(htab))[i]
				#	define	de_stack				((char_type *)&(htab[hSize*8-1]))
				#	define	clear_htab()			memset(htab, -1, sizeof(htab))
				#	define	clear_tab_prefixof()	memset(codetab, 0, 256);
				*/
				// x := byte(finchar)
				// stackp--
				// *stackp = x
				// htab[]int64
				// finchar is int type trunc to char, stuffed into int64 arrary

				stackNdx--
				g_htab[stackNdx] = byte(finchar)
				code = oldcode
			}
			// Generate output characters in reverse order
			//while ((cmp_code_int)code >= (cmp_code_int)256){
			//	*--stackp = tab_suffixof(code);
			//	code = tab_prefixof(code);
			//}
			for {
				if code < 256 {
					break
				}

				//	*--stackp = htab[code]
				stackNdx--
				g_htab[stackNdx] = g_htab[code]
				code = int64(g_codetab[code])
			}

			//*--stackp =	(char_type)(finchar = tab_suffixof(code));
			finchar = int(g_htab[code])
			stackNdx--
			g_htab[stackNdx] = byte(finchar)

			// seems ok up to here, output tokens match so far
			// compress42 has empty htab at end - we have a full one.  WHY?
			// --- line 1792

			{ // brace 2 stuff here...
				i := (hSize*8 - 1) - stackNdx

				tmp := i + outpos
				if tmp >= oBufSize {
					for { // do while ((i=de_stack-stackp))>0
						if i > (oBufSize - outpos) {
							i = oBufSize - outpos
						}
						//	void *memcpy(void *dest, const void *src, size_t n);
						if i > 0 {
							//  memcpy(outbuf+outpos, stackp, i)
							j := outpos
							k := 0
							for {
								if k >= i { // not likely but i could be zero
									break
								}

								g_outbuf[j] = g_htab[stackNdx+k]
								j++
								k++
							}
							outpos += i
						}

						if outpos >= oBufSize { // output buffer needs to be flushed
							outBuf = append(outBuf, g_outbuf[0:outpos]...)
							outpos = 0
						}

						stackNdx += i
						i = (hSize*8 - 1) - stackNdx
						if i <= 0 {
							break
						}
					} // end of dowhile
				} else {
					//  memcpy(outbuf+outpos, stackp, i);
					j := outpos
					k := 0
					for {
						if k >= i { // not likely but i could be zero
							break
						}

						g_outbuf[j] = g_htab[stackNdx+k]
						j++
						k++
					}
					outpos += i
				}
			} // brace 2 end

			//	Generate the new entry in code table
			code = free_ent
			if code < maxmaxcode {
				g_codetab[code] = uint16(oldcode) // codetab is uint16
				g_htab[code] = byte(finchar)
				free_ent = code + 1
			}
			oldcode = incode // remember previous code
		} // end of while inbits > posbits
		bytes_in += rsize
		if rsize <= 0 {
			break
		}
	} // end of do while (rsize > 0)
	// man2write -> ssize_t write(int fd, const void *buf, size_t count);

	// flush remaining output
	if outpos > 0 {
		outBuf = append(outBuf, g_outbuf[0:outpos]...)
	}

	// BUG(mdr): <kludge alert>
	outBuf[0] = firstChar
	// </kludge>

	byteReader := bytes.NewReader(outBuf)
	return byteReader, nil
}
