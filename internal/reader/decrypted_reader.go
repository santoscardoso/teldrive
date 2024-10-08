package reader

import (
	"context"
	"fmt"
	"io"

	"github.com/divyam234/teldrive/internal/cache"
	"github.com/divyam234/teldrive/internal/config"
	"github.com/divyam234/teldrive/internal/crypt"
	"github.com/divyam234/teldrive/internal/tgc"
	"github.com/divyam234/teldrive/pkg/schemas"
	"github.com/divyam234/teldrive/pkg/types"
	"github.com/gotd/td/tg"
)

type decrpytedReader struct {
	ctx         context.Context
	file        *schemas.FileOutFull
	parts       []types.Part
	ranges      []types.Range
	pos         int
	reader      io.ReadCloser
	limit       int64
	config      *config.TGConfig
	worker      *tgc.StreamWorker
	client      *tg.Client
	concurrency int
	cache       cache.Cacher
}

func NewDecryptedReader(
	ctx context.Context,
	client *tg.Client,
	worker *tgc.StreamWorker,
	cache cache.Cacher,
	file *schemas.FileOutFull,
	parts []types.Part,
	start,
	end int64,
	config *config.TGConfig,
	concurrency int,
) (*decrpytedReader, error) {

	r := &decrpytedReader{
		ctx:         ctx,
		parts:       parts,
		file:        file,
		limit:       end - start + 1,
		ranges:      calculatePartByteRanges(start, end, parts[0].DecryptedSize),
		config:      config,
		client:      client,
		worker:      worker,
		concurrency: concurrency,
		cache:       cache,
	}
	res, err := r.nextPart()

	if err != nil {
		return nil, err
	}

	r.reader = res

	return r, nil

}

func (r *decrpytedReader) Read(p []byte) (int, error) {

	if r.limit <= 0 {
		return 0, io.EOF
	}

	n, err := r.reader.Read(p)

	if err == io.EOF {
		if r.limit > 0 {
			err = nil
			if r.reader != nil {
				r.reader.Close()
			}
		}
		r.pos++
		if r.pos < len(r.ranges) {
			r.reader, err = r.nextPart()

		}
	}
	r.limit -= int64(n)
	return n, err
}
func (r *decrpytedReader) Close() (err error) {
	if r.reader != nil {
		err = r.reader.Close()
		r.reader = nil
		return err
	}
	return nil
}

func (r *decrpytedReader) nextPart() (io.ReadCloser, error) {

	start := r.ranges[r.pos].Start
	end := r.ranges[r.pos].End
	salt := r.parts[r.ranges[r.pos].PartNo].Salt
	cipher, _ := crypt.NewCipher(r.config.Uploads.EncryptionKey, salt)

	return cipher.DecryptDataSeek(r.ctx,
		func(ctx context.Context,
			underlyingOffset,
			underlyingLimit int64) (io.ReadCloser, error) {
			var end int64

			if underlyingLimit >= 0 {
				end = min(r.parts[r.ranges[r.pos].PartNo].Size-1, underlyingOffset+underlyingLimit-1)
			}
			partID := r.parts[r.ranges[r.pos].PartNo].ID

			chunkSrc := &chunkSource{
				channelID:   r.file.ChannelID,
				partID:      partID,
				client:      r.client,
				concurrency: r.concurrency,
				cache:       r.cache,
				key:         fmt.Sprintf("files:location:%s:%d", r.file.Id, partID),
				worker:      r.worker,
			}
			if r.concurrency < 2 {
				return newTGReader(r.ctx, underlyingOffset, end, chunkSrc)
			}
			return newTGMultiReader(r.ctx, underlyingOffset, end, r.config, chunkSrc)

		}, start, end-start+1)

}
