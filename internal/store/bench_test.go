package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"strings"
	"testing"
)

func makeParagraph(label byte, size int) []byte {
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = label
	}
	return buf
}

func makeSemiCompressibleData(totalSize int) []byte {
	chunkSize := totalSize / 4

	paraA := makeParagraph('A', chunkSize)
	paraB := makeParagraph('B', chunkSize)
	paraC := makeParagraph('C', chunkSize)
	paraD := makeParagraph('D', chunkSize)

	var sb strings.Builder
	sb.Grow(totalSize)
	for sb.Len() < totalSize {
		sb.Write(paraA)
		if sb.Len() >= totalSize {
			break
		}
		sb.Write(paraB)
		if sb.Len() >= totalSize {
			break
		}
		sb.Write(paraC)
		if sb.Len() >= totalSize {
			break
		}
		sb.Write(paraD)
	}
	return []byte(sb.String()[:totalSize])
}

func BenchmarkCAS_Store_1MB(b *testing.B) {
	ctx := context.Background()

	b.Run("Blob_1MB", func(b *testing.B) {
		data := makeSemiCompressibleData(1048576) // 1MB
		cas := NewCAS(b.TempDir())

		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			_, err := cas.Store(ctx, bytes.NewReader(data))
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("Blob_1KB", func(b *testing.B) {
		data := make([]byte, 1024)
		_, err := rand.Read(data)
		if err != nil {
			b.Fatal(err)
		}
		cas := NewCAS(b.TempDir())

		b.SetBytes(1024)
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			_, err := cas.Store(ctx, bytes.NewReader(data))
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkCAS_Open_1MB(b *testing.B) {
	ctx := context.Background()

	data := makeSemiCompressibleData(1048576)
	cas := NewCAS(b.TempDir())

	hash, err := cas.Store(ctx, bytes.NewReader(data))
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rc, err := cas.Open(ctx, hash)
		if err != nil {
			b.Fatal(err)
		}
		n, err := io.Copy(io.Discard, rc)
		rc.Close()
		if err != nil {
			b.Fatal(err)
		}
		if n != int64(len(data)) {
			b.Fatalf("read %d bytes, want %d", n, len(data))
		}
	}
}
