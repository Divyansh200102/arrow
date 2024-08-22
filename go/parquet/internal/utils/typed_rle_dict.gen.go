// Code generated by typed_rle_dict.gen.go.tmpl. DO NOT EDIT.

// Licensed to the Apache Software Foundation (ASF) under one
// or more contributor license agreements.  See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership.  The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package utils

import (
	"github.com/apache/arrow/go/v18/internal/bitutils"
	"github.com/apache/arrow/go/v18/internal/utils"
	"github.com/apache/arrow/go/v18/parquet"
	"golang.org/x/xerrors"
)

func (r *RleDecoder) GetBatchWithDictSpacedInt32(dc DictionaryConverter, vals []int32, nullCount int, validBits []byte, validBitsOffset int64) (totalProcessed int, err error) {
	if nullCount == 0 {
		return r.GetBatchWithDictInt32(dc, vals)
	}

	var (
		blockCounter = bitutils.NewBitBlockCounter(validBits, validBitsOffset, int64(len(vals)))
		processed    = 0
		block        bitutils.BitBlockCount
	)

	for {
		block = blockCounter.NextFourWords()
		if block.Len == 0 {
			break
		}

		switch {
		case block.AllSet():
			processed, err = r.GetBatchWithDictInt32(dc, vals[:block.Len])
		case block.NoneSet():
			dc.FillZero(vals[:block.Len])
			processed = int(block.Len)
		default:
			processed, err = r.getspacedInt32(dc, vals, int(block.Len), int(block.Len)-int(block.Popcnt), validBits, validBitsOffset)
		}

		if err != nil {
			break
		}

		totalProcessed += processed
		vals = vals[int(block.Len):]
		validBitsOffset += int64(block.Len)
		if processed != int(block.Len) {
			break
		}
	}
	return
}

func (r *RleDecoder) getspacedInt32(dc DictionaryConverter, vals []int32, batchSize, nullCount int, validBits []byte, validBitsOffset int64) (int, error) {
	if nullCount == batchSize {
		dc.FillZero(vals[:batchSize])
		return batchSize, nil
	}

	read := 0
	remain := batchSize - nullCount

	const bufferSize = 1024
	var indexbuffer [bufferSize]IndexType

	// assume no bits to start
	bitReader := bitutils.NewBitRunReader(validBits, validBitsOffset, int64(batchSize))
	validRun := bitReader.NextRun()
	for read < batchSize {
		if validRun.Len == 0 {
			validRun = bitReader.NextRun()
		}

		if !validRun.Set {
			dc.FillZero(vals[:int(validRun.Len)])
			vals = vals[int(validRun.Len):]
			read += int(validRun.Len)
			validRun.Len = 0
			continue
		}

		if r.repCount == 0 && r.litCount == 0 {
			if !r.Next() {
				return read, nil
			}
		}

		var batch int
		switch {
		case r.repCount > 0:
			batch, remain, validRun = r.consumeRepeatCounts(read, batchSize, remain, validRun, bitReader)
			current := IndexType(r.curVal)
			if !dc.IsValid(current) {
				return read, nil
			}
			dc.Fill(vals[:batch], current)
		case r.litCount > 0:
			var (
				litread int
				skipped int
				err     error
			)
			litread, skipped, validRun, err = r.consumeLiteralsInt32(dc, vals, remain, indexbuffer[:], validRun, bitReader)
			if err != nil {
				return read, err
			}
			batch = litread + skipped
			remain -= litread
		}

		vals = vals[batch:]
		read += batch
	}
	return read, nil
}

func (r *RleDecoder) consumeLiteralsInt32(dc DictionaryConverter, vals []int32, remain int, buf []IndexType, run bitutils.BitRun, bitRdr bitutils.BitRunReader) (int, int, bitutils.BitRun, error) {
	batch := utils.Min(utils.Min(remain, int(r.litCount)), len(buf))
	buf = buf[:batch]

	n, _ := r.r.GetBatchIndex(uint(r.bitWidth), buf)
	if n != batch {
		return 0, 0, run, xerrors.New("was not able to retrieve correct number of indexes")
	}

	if !dc.IsValid(buf...) {
		return 0, 0, run, xerrors.New("invalid index values found for dictionary converter")
	}

	var (
		read    int
		skipped int
	)
	for read < batch {
		if run.Set {
			updateSize := utils.Min(batch-read, int(run.Len))
			if err := dc.Copy(vals, buf[read:read+updateSize]); err != nil {
				return 0, 0, run, err
			}
			read += updateSize
			vals = vals[updateSize:]
			run.Len -= int64(updateSize)
		} else {
			dc.FillZero(vals[:int(run.Len)])
			vals = vals[int(run.Len):]
			skipped += int(run.Len)
			run.Len = 0
		}
		if run.Len == 0 {
			run = bitRdr.NextRun()
		}
	}
	r.litCount -= int32(batch)
	return read, skipped, run, nil
}

func (r *RleDecoder) GetBatchWithDictInt32(dc DictionaryConverter, vals []int32) (int, error) {
	var (
		read        = 0
		size        = len(vals)
		indexbuffer [1024]IndexType
	)

	for read < size {
		remain := size - read

		switch {
		case r.repCount > 0:
			idx := IndexType(r.curVal)
			if !dc.IsValid(idx) {
				return read, nil
			}
			batch := utils.Min(remain, int(r.repCount))
			if err := dc.Fill(vals[:batch], idx); err != nil {
				return read, err
			}
			r.repCount -= int32(batch)
			read += batch
			vals = vals[batch:]
		case r.litCount > 0:
			litbatch := utils.Min(utils.Min(remain, int(r.litCount)), 1024)
			buf := indexbuffer[:litbatch]
			n, _ := r.r.GetBatchIndex(uint(r.bitWidth), buf)
			if n != litbatch {
				return read, nil
			}
			if !dc.IsValid(buf...) {
				return read, nil
			}
			if err := dc.Copy(vals, buf); err != nil {
				return read, nil
			}
			r.litCount -= int32(litbatch)
			read += litbatch
			vals = vals[litbatch:]
		default:
			if !r.Next() {
				return read, nil
			}
		}
	}

	return read, nil
}

func (r *RleDecoder) GetBatchWithDictSpacedInt64(dc DictionaryConverter, vals []int64, nullCount int, validBits []byte, validBitsOffset int64) (totalProcessed int, err error) {
	if nullCount == 0 {
		return r.GetBatchWithDictInt64(dc, vals)
	}

	var (
		blockCounter = bitutils.NewBitBlockCounter(validBits, validBitsOffset, int64(len(vals)))
		processed    = 0
		block        bitutils.BitBlockCount
	)

	for {
		block = blockCounter.NextFourWords()
		if block.Len == 0 {
			break
		}

		switch {
		case block.AllSet():
			processed, err = r.GetBatchWithDictInt64(dc, vals[:block.Len])
		case block.NoneSet():
			dc.FillZero(vals[:block.Len])
			processed = int(block.Len)
		default:
			processed, err = r.getspacedInt64(dc, vals, int(block.Len), int(block.Len)-int(block.Popcnt), validBits, validBitsOffset)
		}

		if err != nil {
			break
		}

		totalProcessed += processed
		vals = vals[int(block.Len):]
		validBitsOffset += int64(block.Len)
		if processed != int(block.Len) {
			break
		}
	}
	return
}

func (r *RleDecoder) getspacedInt64(dc DictionaryConverter, vals []int64, batchSize, nullCount int, validBits []byte, validBitsOffset int64) (int, error) {
	if nullCount == batchSize {
		dc.FillZero(vals[:batchSize])
		return batchSize, nil
	}

	read := 0
	remain := batchSize - nullCount

	const bufferSize = 1024
	var indexbuffer [bufferSize]IndexType

	// assume no bits to start
	bitReader := bitutils.NewBitRunReader(validBits, validBitsOffset, int64(batchSize))
	validRun := bitReader.NextRun()
	for read < batchSize {
		if validRun.Len == 0 {
			validRun = bitReader.NextRun()
		}

		if !validRun.Set {
			dc.FillZero(vals[:int(validRun.Len)])
			vals = vals[int(validRun.Len):]
			read += int(validRun.Len)
			validRun.Len = 0
			continue
		}

		if r.repCount == 0 && r.litCount == 0 {
			if !r.Next() {
				return read, nil
			}
		}

		var batch int
		switch {
		case r.repCount > 0:
			batch, remain, validRun = r.consumeRepeatCounts(read, batchSize, remain, validRun, bitReader)
			current := IndexType(r.curVal)
			if !dc.IsValid(current) {
				return read, nil
			}
			dc.Fill(vals[:batch], current)
		case r.litCount > 0:
			var (
				litread int
				skipped int
				err     error
			)
			litread, skipped, validRun, err = r.consumeLiteralsInt64(dc, vals, remain, indexbuffer[:], validRun, bitReader)
			if err != nil {
				return read, err
			}
			batch = litread + skipped
			remain -= litread
		}

		vals = vals[batch:]
		read += batch
	}
	return read, nil
}

func (r *RleDecoder) consumeLiteralsInt64(dc DictionaryConverter, vals []int64, remain int, buf []IndexType, run bitutils.BitRun, bitRdr bitutils.BitRunReader) (int, int, bitutils.BitRun, error) {
	batch := utils.Min(utils.Min(remain, int(r.litCount)), len(buf))
	buf = buf[:batch]

	n, _ := r.r.GetBatchIndex(uint(r.bitWidth), buf)
	if n != batch {
		return 0, 0, run, xerrors.New("was not able to retrieve correct number of indexes")
	}

	if !dc.IsValid(buf...) {
		return 0, 0, run, xerrors.New("invalid index values found for dictionary converter")
	}

	var (
		read    int
		skipped int
	)
	for read < batch {
		if run.Set {
			updateSize := utils.Min(batch-read, int(run.Len))
			if err := dc.Copy(vals, buf[read:read+updateSize]); err != nil {
				return 0, 0, run, err
			}
			read += updateSize
			vals = vals[updateSize:]
			run.Len -= int64(updateSize)
		} else {
			dc.FillZero(vals[:int(run.Len)])
			vals = vals[int(run.Len):]
			skipped += int(run.Len)
			run.Len = 0
		}
		if run.Len == 0 {
			run = bitRdr.NextRun()
		}
	}
	r.litCount -= int32(batch)
	return read, skipped, run, nil
}

func (r *RleDecoder) GetBatchWithDictInt64(dc DictionaryConverter, vals []int64) (int, error) {
	var (
		read        = 0
		size        = len(vals)
		indexbuffer [1024]IndexType
	)

	for read < size {
		remain := size - read

		switch {
		case r.repCount > 0:
			idx := IndexType(r.curVal)
			if !dc.IsValid(idx) {
				return read, nil
			}
			batch := utils.Min(remain, int(r.repCount))
			if err := dc.Fill(vals[:batch], idx); err != nil {
				return read, err
			}
			r.repCount -= int32(batch)
			read += batch
			vals = vals[batch:]
		case r.litCount > 0:
			litbatch := utils.Min(utils.Min(remain, int(r.litCount)), 1024)
			buf := indexbuffer[:litbatch]
			n, _ := r.r.GetBatchIndex(uint(r.bitWidth), buf)
			if n != litbatch {
				return read, nil
			}
			if !dc.IsValid(buf...) {
				return read, nil
			}
			if err := dc.Copy(vals, buf); err != nil {
				return read, nil
			}
			r.litCount -= int32(litbatch)
			read += litbatch
			vals = vals[litbatch:]
		default:
			if !r.Next() {
				return read, nil
			}
		}
	}

	return read, nil
}

func (r *RleDecoder) GetBatchWithDictSpacedInt96(dc DictionaryConverter, vals []parquet.Int96, nullCount int, validBits []byte, validBitsOffset int64) (totalProcessed int, err error) {
	if nullCount == 0 {
		return r.GetBatchWithDictInt96(dc, vals)
	}

	var (
		blockCounter = bitutils.NewBitBlockCounter(validBits, validBitsOffset, int64(len(vals)))
		processed    = 0
		block        bitutils.BitBlockCount
	)

	for {
		block = blockCounter.NextFourWords()
		if block.Len == 0 {
			break
		}

		switch {
		case block.AllSet():
			processed, err = r.GetBatchWithDictInt96(dc, vals[:block.Len])
		case block.NoneSet():
			dc.FillZero(vals[:block.Len])
			processed = int(block.Len)
		default:
			processed, err = r.getspacedInt96(dc, vals, int(block.Len), int(block.Len)-int(block.Popcnt), validBits, validBitsOffset)
		}

		if err != nil {
			break
		}

		totalProcessed += processed
		vals = vals[int(block.Len):]
		validBitsOffset += int64(block.Len)
		if processed != int(block.Len) {
			break
		}
	}
	return
}

func (r *RleDecoder) getspacedInt96(dc DictionaryConverter, vals []parquet.Int96, batchSize, nullCount int, validBits []byte, validBitsOffset int64) (int, error) {
	if nullCount == batchSize {
		dc.FillZero(vals[:batchSize])
		return batchSize, nil
	}

	read := 0
	remain := batchSize - nullCount

	const bufferSize = 1024
	var indexbuffer [bufferSize]IndexType

	// assume no bits to start
	bitReader := bitutils.NewBitRunReader(validBits, validBitsOffset, int64(batchSize))
	validRun := bitReader.NextRun()
	for read < batchSize {
		if validRun.Len == 0 {
			validRun = bitReader.NextRun()
		}

		if !validRun.Set {
			dc.FillZero(vals[:int(validRun.Len)])
			vals = vals[int(validRun.Len):]
			read += int(validRun.Len)
			validRun.Len = 0
			continue
		}

		if r.repCount == 0 && r.litCount == 0 {
			if !r.Next() {
				return read, nil
			}
		}

		var batch int
		switch {
		case r.repCount > 0:
			batch, remain, validRun = r.consumeRepeatCounts(read, batchSize, remain, validRun, bitReader)
			current := IndexType(r.curVal)
			if !dc.IsValid(current) {
				return read, nil
			}
			dc.Fill(vals[:batch], current)
		case r.litCount > 0:
			var (
				litread int
				skipped int
				err     error
			)
			litread, skipped, validRun, err = r.consumeLiteralsInt96(dc, vals, remain, indexbuffer[:], validRun, bitReader)
			if err != nil {
				return read, err
			}
			batch = litread + skipped
			remain -= litread
		}

		vals = vals[batch:]
		read += batch
	}
	return read, nil
}

func (r *RleDecoder) consumeLiteralsInt96(dc DictionaryConverter, vals []parquet.Int96, remain int, buf []IndexType, run bitutils.BitRun, bitRdr bitutils.BitRunReader) (int, int, bitutils.BitRun, error) {
	batch := utils.Min(utils.Min(remain, int(r.litCount)), len(buf))
	buf = buf[:batch]

	n, _ := r.r.GetBatchIndex(uint(r.bitWidth), buf)
	if n != batch {
		return 0, 0, run, xerrors.New("was not able to retrieve correct number of indexes")
	}

	if !dc.IsValid(buf...) {
		return 0, 0, run, xerrors.New("invalid index values found for dictionary converter")
	}

	var (
		read    int
		skipped int
	)
	for read < batch {
		if run.Set {
			updateSize := utils.Min(batch-read, int(run.Len))
			if err := dc.Copy(vals, buf[read:read+updateSize]); err != nil {
				return 0, 0, run, err
			}
			read += updateSize
			vals = vals[updateSize:]
			run.Len -= int64(updateSize)
		} else {
			dc.FillZero(vals[:int(run.Len)])
			vals = vals[int(run.Len):]
			skipped += int(run.Len)
			run.Len = 0
		}
		if run.Len == 0 {
			run = bitRdr.NextRun()
		}
	}
	r.litCount -= int32(batch)
	return read, skipped, run, nil
}

func (r *RleDecoder) GetBatchWithDictInt96(dc DictionaryConverter, vals []parquet.Int96) (int, error) {
	var (
		read        = 0
		size        = len(vals)
		indexbuffer [1024]IndexType
	)

	for read < size {
		remain := size - read

		switch {
		case r.repCount > 0:
			idx := IndexType(r.curVal)
			if !dc.IsValid(idx) {
				return read, nil
			}
			batch := utils.Min(remain, int(r.repCount))
			if err := dc.Fill(vals[:batch], idx); err != nil {
				return read, err
			}
			r.repCount -= int32(batch)
			read += batch
			vals = vals[batch:]
		case r.litCount > 0:
			litbatch := utils.Min(utils.Min(remain, int(r.litCount)), 1024)
			buf := indexbuffer[:litbatch]
			n, _ := r.r.GetBatchIndex(uint(r.bitWidth), buf)
			if n != litbatch {
				return read, nil
			}
			if !dc.IsValid(buf...) {
				return read, nil
			}
			if err := dc.Copy(vals, buf); err != nil {
				return read, nil
			}
			r.litCount -= int32(litbatch)
			read += litbatch
			vals = vals[litbatch:]
		default:
			if !r.Next() {
				return read, nil
			}
		}
	}

	return read, nil
}

func (r *RleDecoder) GetBatchWithDictSpacedFloat32(dc DictionaryConverter, vals []float32, nullCount int, validBits []byte, validBitsOffset int64) (totalProcessed int, err error) {
	if nullCount == 0 {
		return r.GetBatchWithDictFloat32(dc, vals)
	}

	var (
		blockCounter = bitutils.NewBitBlockCounter(validBits, validBitsOffset, int64(len(vals)))
		processed    = 0
		block        bitutils.BitBlockCount
	)

	for {
		block = blockCounter.NextFourWords()
		if block.Len == 0 {
			break
		}

		switch {
		case block.AllSet():
			processed, err = r.GetBatchWithDictFloat32(dc, vals[:block.Len])
		case block.NoneSet():
			dc.FillZero(vals[:block.Len])
			processed = int(block.Len)
		default:
			processed, err = r.getspacedFloat32(dc, vals, int(block.Len), int(block.Len)-int(block.Popcnt), validBits, validBitsOffset)
		}

		if err != nil {
			break
		}

		totalProcessed += processed
		vals = vals[int(block.Len):]
		validBitsOffset += int64(block.Len)
		if processed != int(block.Len) {
			break
		}
	}
	return
}

func (r *RleDecoder) getspacedFloat32(dc DictionaryConverter, vals []float32, batchSize, nullCount int, validBits []byte, validBitsOffset int64) (int, error) {
	if nullCount == batchSize {
		dc.FillZero(vals[:batchSize])
		return batchSize, nil
	}

	read := 0
	remain := batchSize - nullCount

	const bufferSize = 1024
	var indexbuffer [bufferSize]IndexType

	// assume no bits to start
	bitReader := bitutils.NewBitRunReader(validBits, validBitsOffset, int64(batchSize))
	validRun := bitReader.NextRun()
	for read < batchSize {
		if validRun.Len == 0 {
			validRun = bitReader.NextRun()
		}

		if !validRun.Set {
			dc.FillZero(vals[:int(validRun.Len)])
			vals = vals[int(validRun.Len):]
			read += int(validRun.Len)
			validRun.Len = 0
			continue
		}

		if r.repCount == 0 && r.litCount == 0 {
			if !r.Next() {
				return read, nil
			}
		}

		var batch int
		switch {
		case r.repCount > 0:
			batch, remain, validRun = r.consumeRepeatCounts(read, batchSize, remain, validRun, bitReader)
			current := IndexType(r.curVal)
			if !dc.IsValid(current) {
				return read, nil
			}
			dc.Fill(vals[:batch], current)
		case r.litCount > 0:
			var (
				litread int
				skipped int
				err     error
			)
			litread, skipped, validRun, err = r.consumeLiteralsFloat32(dc, vals, remain, indexbuffer[:], validRun, bitReader)
			if err != nil {
				return read, err
			}
			batch = litread + skipped
			remain -= litread
		}

		vals = vals[batch:]
		read += batch
	}
	return read, nil
}

func (r *RleDecoder) consumeLiteralsFloat32(dc DictionaryConverter, vals []float32, remain int, buf []IndexType, run bitutils.BitRun, bitRdr bitutils.BitRunReader) (int, int, bitutils.BitRun, error) {
	batch := utils.Min(utils.Min(remain, int(r.litCount)), len(buf))
	buf = buf[:batch]

	n, _ := r.r.GetBatchIndex(uint(r.bitWidth), buf)
	if n != batch {
		return 0, 0, run, xerrors.New("was not able to retrieve correct number of indexes")
	}

	if !dc.IsValid(buf...) {
		return 0, 0, run, xerrors.New("invalid index values found for dictionary converter")
	}

	var (
		read    int
		skipped int
	)
	for read < batch {
		if run.Set {
			updateSize := utils.Min(batch-read, int(run.Len))
			if err := dc.Copy(vals, buf[read:read+updateSize]); err != nil {
				return 0, 0, run, err
			}
			read += updateSize
			vals = vals[updateSize:]
			run.Len -= int64(updateSize)
		} else {
			dc.FillZero(vals[:int(run.Len)])
			vals = vals[int(run.Len):]
			skipped += int(run.Len)
			run.Len = 0
		}
		if run.Len == 0 {
			run = bitRdr.NextRun()
		}
	}
	r.litCount -= int32(batch)
	return read, skipped, run, nil
}

func (r *RleDecoder) GetBatchWithDictFloat32(dc DictionaryConverter, vals []float32) (int, error) {
	var (
		read        = 0
		size        = len(vals)
		indexbuffer [1024]IndexType
	)

	for read < size {
		remain := size - read

		switch {
		case r.repCount > 0:
			idx := IndexType(r.curVal)
			if !dc.IsValid(idx) {
				return read, nil
			}
			batch := utils.Min(remain, int(r.repCount))
			if err := dc.Fill(vals[:batch], idx); err != nil {
				return read, err
			}
			r.repCount -= int32(batch)
			read += batch
			vals = vals[batch:]
		case r.litCount > 0:
			litbatch := utils.Min(utils.Min(remain, int(r.litCount)), 1024)
			buf := indexbuffer[:litbatch]
			n, _ := r.r.GetBatchIndex(uint(r.bitWidth), buf)
			if n != litbatch {
				return read, nil
			}
			if !dc.IsValid(buf...) {
				return read, nil
			}
			if err := dc.Copy(vals, buf); err != nil {
				return read, nil
			}
			r.litCount -= int32(litbatch)
			read += litbatch
			vals = vals[litbatch:]
		default:
			if !r.Next() {
				return read, nil
			}
		}
	}

	return read, nil
}

func (r *RleDecoder) GetBatchWithDictSpacedFloat64(dc DictionaryConverter, vals []float64, nullCount int, validBits []byte, validBitsOffset int64) (totalProcessed int, err error) {
	if nullCount == 0 {
		return r.GetBatchWithDictFloat64(dc, vals)
	}

	var (
		blockCounter = bitutils.NewBitBlockCounter(validBits, validBitsOffset, int64(len(vals)))
		processed    = 0
		block        bitutils.BitBlockCount
	)

	for {
		block = blockCounter.NextFourWords()
		if block.Len == 0 {
			break
		}

		switch {
		case block.AllSet():
			processed, err = r.GetBatchWithDictFloat64(dc, vals[:block.Len])
		case block.NoneSet():
			dc.FillZero(vals[:block.Len])
			processed = int(block.Len)
		default:
			processed, err = r.getspacedFloat64(dc, vals, int(block.Len), int(block.Len)-int(block.Popcnt), validBits, validBitsOffset)
		}

		if err != nil {
			break
		}

		totalProcessed += processed
		vals = vals[int(block.Len):]
		validBitsOffset += int64(block.Len)
		if processed != int(block.Len) {
			break
		}
	}
	return
}

func (r *RleDecoder) getspacedFloat64(dc DictionaryConverter, vals []float64, batchSize, nullCount int, validBits []byte, validBitsOffset int64) (int, error) {
	if nullCount == batchSize {
		dc.FillZero(vals[:batchSize])
		return batchSize, nil
	}

	read := 0
	remain := batchSize - nullCount

	const bufferSize = 1024
	var indexbuffer [bufferSize]IndexType

	// assume no bits to start
	bitReader := bitutils.NewBitRunReader(validBits, validBitsOffset, int64(batchSize))
	validRun := bitReader.NextRun()
	for read < batchSize {
		if validRun.Len == 0 {
			validRun = bitReader.NextRun()
		}

		if !validRun.Set {
			dc.FillZero(vals[:int(validRun.Len)])
			vals = vals[int(validRun.Len):]
			read += int(validRun.Len)
			validRun.Len = 0
			continue
		}

		if r.repCount == 0 && r.litCount == 0 {
			if !r.Next() {
				return read, nil
			}
		}

		var batch int
		switch {
		case r.repCount > 0:
			batch, remain, validRun = r.consumeRepeatCounts(read, batchSize, remain, validRun, bitReader)
			current := IndexType(r.curVal)
			if !dc.IsValid(current) {
				return read, nil
			}
			dc.Fill(vals[:batch], current)
		case r.litCount > 0:
			var (
				litread int
				skipped int
				err     error
			)
			litread, skipped, validRun, err = r.consumeLiteralsFloat64(dc, vals, remain, indexbuffer[:], validRun, bitReader)
			if err != nil {
				return read, err
			}
			batch = litread + skipped
			remain -= litread
		}

		vals = vals[batch:]
		read += batch
	}
	return read, nil
}

func (r *RleDecoder) consumeLiteralsFloat64(dc DictionaryConverter, vals []float64, remain int, buf []IndexType, run bitutils.BitRun, bitRdr bitutils.BitRunReader) (int, int, bitutils.BitRun, error) {
	batch := utils.Min(utils.Min(remain, int(r.litCount)), len(buf))
	buf = buf[:batch]

	n, _ := r.r.GetBatchIndex(uint(r.bitWidth), buf)
	if n != batch {
		return 0, 0, run, xerrors.New("was not able to retrieve correct number of indexes")
	}

	if !dc.IsValid(buf...) {
		return 0, 0, run, xerrors.New("invalid index values found for dictionary converter")
	}

	var (
		read    int
		skipped int
	)
	for read < batch {
		if run.Set {
			updateSize := utils.Min(batch-read, int(run.Len))
			if err := dc.Copy(vals, buf[read:read+updateSize]); err != nil {
				return 0, 0, run, err
			}
			read += updateSize
			vals = vals[updateSize:]
			run.Len -= int64(updateSize)
		} else {
			dc.FillZero(vals[:int(run.Len)])
			vals = vals[int(run.Len):]
			skipped += int(run.Len)
			run.Len = 0
		}
		if run.Len == 0 {
			run = bitRdr.NextRun()
		}
	}
	r.litCount -= int32(batch)
	return read, skipped, run, nil
}

func (r *RleDecoder) GetBatchWithDictFloat64(dc DictionaryConverter, vals []float64) (int, error) {
	var (
		read        = 0
		size        = len(vals)
		indexbuffer [1024]IndexType
	)

	for read < size {
		remain := size - read

		switch {
		case r.repCount > 0:
			idx := IndexType(r.curVal)
			if !dc.IsValid(idx) {
				return read, nil
			}
			batch := utils.Min(remain, int(r.repCount))
			if err := dc.Fill(vals[:batch], idx); err != nil {
				return read, err
			}
			r.repCount -= int32(batch)
			read += batch
			vals = vals[batch:]
		case r.litCount > 0:
			litbatch := utils.Min(utils.Min(remain, int(r.litCount)), 1024)
			buf := indexbuffer[:litbatch]
			n, _ := r.r.GetBatchIndex(uint(r.bitWidth), buf)
			if n != litbatch {
				return read, nil
			}
			if !dc.IsValid(buf...) {
				return read, nil
			}
			if err := dc.Copy(vals, buf); err != nil {
				return read, nil
			}
			r.litCount -= int32(litbatch)
			read += litbatch
			vals = vals[litbatch:]
		default:
			if !r.Next() {
				return read, nil
			}
		}
	}

	return read, nil
}

func (r *RleDecoder) GetBatchWithDictSpacedByteArray(dc DictionaryConverter, vals []parquet.ByteArray, nullCount int, validBits []byte, validBitsOffset int64) (totalProcessed int, err error) {
	if nullCount == 0 {
		return r.GetBatchWithDictByteArray(dc, vals)
	}

	var (
		blockCounter = bitutils.NewBitBlockCounter(validBits, validBitsOffset, int64(len(vals)))
		processed    = 0
		block        bitutils.BitBlockCount
	)

	for {
		block = blockCounter.NextFourWords()
		if block.Len == 0 {
			break
		}

		switch {
		case block.AllSet():
			processed, err = r.GetBatchWithDictByteArray(dc, vals[:block.Len])
		case block.NoneSet():
			dc.FillZero(vals[:block.Len])
			processed = int(block.Len)
		default:
			processed, err = r.getspacedByteArray(dc, vals, int(block.Len), int(block.Len)-int(block.Popcnt), validBits, validBitsOffset)
		}

		if err != nil {
			break
		}

		totalProcessed += processed
		vals = vals[int(block.Len):]
		validBitsOffset += int64(block.Len)
		if processed != int(block.Len) {
			break
		}
	}
	return
}

func (r *RleDecoder) getspacedByteArray(dc DictionaryConverter, vals []parquet.ByteArray, batchSize, nullCount int, validBits []byte, validBitsOffset int64) (int, error) {
	if nullCount == batchSize {
		dc.FillZero(vals[:batchSize])
		return batchSize, nil
	}

	read := 0
	remain := batchSize - nullCount

	const bufferSize = 1024
	var indexbuffer [bufferSize]IndexType

	// assume no bits to start
	bitReader := bitutils.NewBitRunReader(validBits, validBitsOffset, int64(batchSize))
	validRun := bitReader.NextRun()
	for read < batchSize {
		if validRun.Len == 0 {
			validRun = bitReader.NextRun()
		}

		if !validRun.Set {
			dc.FillZero(vals[:int(validRun.Len)])
			vals = vals[int(validRun.Len):]
			read += int(validRun.Len)
			validRun.Len = 0
			continue
		}

		if r.repCount == 0 && r.litCount == 0 {
			if !r.Next() {
				return read, nil
			}
		}

		var batch int
		switch {
		case r.repCount > 0:
			batch, remain, validRun = r.consumeRepeatCounts(read, batchSize, remain, validRun, bitReader)
			current := IndexType(r.curVal)
			if !dc.IsValid(current) {
				return read, nil
			}
			dc.Fill(vals[:batch], current)
		case r.litCount > 0:
			var (
				litread int
				skipped int
				err     error
			)
			litread, skipped, validRun, err = r.consumeLiteralsByteArray(dc, vals, remain, indexbuffer[:], validRun, bitReader)
			if err != nil {
				return read, err
			}
			batch = litread + skipped
			remain -= litread
		}

		vals = vals[batch:]
		read += batch
	}
	return read, nil
}

func (r *RleDecoder) consumeLiteralsByteArray(dc DictionaryConverter, vals []parquet.ByteArray, remain int, buf []IndexType, run bitutils.BitRun, bitRdr bitutils.BitRunReader) (int, int, bitutils.BitRun, error) {
	batch := utils.Min(utils.Min(remain, int(r.litCount)), len(buf))
	buf = buf[:batch]

	n, _ := r.r.GetBatchIndex(uint(r.bitWidth), buf)
	if n != batch {
		return 0, 0, run, xerrors.New("was not able to retrieve correct number of indexes")
	}

	if !dc.IsValid(buf...) {
		return 0, 0, run, xerrors.New("invalid index values found for dictionary converter")
	}

	var (
		read    int
		skipped int
	)
	for read < batch {
		if run.Set {
			updateSize := utils.Min(batch-read, int(run.Len))
			if err := dc.Copy(vals, buf[read:read+updateSize]); err != nil {
				return 0, 0, run, err
			}
			read += updateSize
			vals = vals[updateSize:]
			run.Len -= int64(updateSize)
		} else {
			dc.FillZero(vals[:int(run.Len)])
			vals = vals[int(run.Len):]
			skipped += int(run.Len)
			run.Len = 0
		}
		if run.Len == 0 {
			run = bitRdr.NextRun()
		}
	}
	r.litCount -= int32(batch)
	return read, skipped, run, nil
}

func (r *RleDecoder) GetBatchWithDictByteArray(dc DictionaryConverter, vals []parquet.ByteArray) (int, error) {
	var (
		read        = 0
		size        = len(vals)
		indexbuffer [1024]IndexType
	)

	for read < size {
		remain := size - read

		switch {
		case r.repCount > 0:
			idx := IndexType(r.curVal)
			if !dc.IsValid(idx) {
				return read, nil
			}
			batch := utils.Min(remain, int(r.repCount))
			if err := dc.Fill(vals[:batch], idx); err != nil {
				return read, err
			}
			r.repCount -= int32(batch)
			read += batch
			vals = vals[batch:]
		case r.litCount > 0:
			litbatch := utils.Min(utils.Min(remain, int(r.litCount)), 1024)
			buf := indexbuffer[:litbatch]
			n, _ := r.r.GetBatchIndex(uint(r.bitWidth), buf)
			if n != litbatch {
				return read, nil
			}
			if !dc.IsValid(buf...) {
				return read, nil
			}
			if err := dc.Copy(vals, buf); err != nil {
				return read, nil
			}
			r.litCount -= int32(litbatch)
			read += litbatch
			vals = vals[litbatch:]
		default:
			if !r.Next() {
				return read, nil
			}
		}
	}

	return read, nil
}

func (r *RleDecoder) GetBatchWithDictSpacedFixedLenByteArray(dc DictionaryConverter, vals []parquet.FixedLenByteArray, nullCount int, validBits []byte, validBitsOffset int64) (totalProcessed int, err error) {
	if nullCount == 0 {
		return r.GetBatchWithDictFixedLenByteArray(dc, vals)
	}

	var (
		blockCounter = bitutils.NewBitBlockCounter(validBits, validBitsOffset, int64(len(vals)))
		processed    = 0
		block        bitutils.BitBlockCount
	)

	for {
		block = blockCounter.NextFourWords()
		if block.Len == 0 {
			break
		}

		switch {
		case block.AllSet():
			processed, err = r.GetBatchWithDictFixedLenByteArray(dc, vals[:block.Len])
		case block.NoneSet():
			dc.FillZero(vals[:block.Len])
			processed = int(block.Len)
		default:
			processed, err = r.getspacedFixedLenByteArray(dc, vals, int(block.Len), int(block.Len)-int(block.Popcnt), validBits, validBitsOffset)
		}

		if err != nil {
			break
		}

		totalProcessed += processed
		vals = vals[int(block.Len):]
		validBitsOffset += int64(block.Len)
		if processed != int(block.Len) {
			break
		}
	}
	return
}

func (r *RleDecoder) getspacedFixedLenByteArray(dc DictionaryConverter, vals []parquet.FixedLenByteArray, batchSize, nullCount int, validBits []byte, validBitsOffset int64) (int, error) {
	if nullCount == batchSize {
		dc.FillZero(vals[:batchSize])
		return batchSize, nil
	}

	read := 0
	remain := batchSize - nullCount

	const bufferSize = 1024
	var indexbuffer [bufferSize]IndexType

	// assume no bits to start
	bitReader := bitutils.NewBitRunReader(validBits, validBitsOffset, int64(batchSize))
	validRun := bitReader.NextRun()
	for read < batchSize {
		if validRun.Len == 0 {
			validRun = bitReader.NextRun()
		}

		if !validRun.Set {
			dc.FillZero(vals[:int(validRun.Len)])
			vals = vals[int(validRun.Len):]
			read += int(validRun.Len)
			validRun.Len = 0
			continue
		}

		if r.repCount == 0 && r.litCount == 0 {
			if !r.Next() {
				return read, nil
			}
		}

		var batch int
		switch {
		case r.repCount > 0:
			batch, remain, validRun = r.consumeRepeatCounts(read, batchSize, remain, validRun, bitReader)
			current := IndexType(r.curVal)
			if !dc.IsValid(current) {
				return read, nil
			}
			dc.Fill(vals[:batch], current)
		case r.litCount > 0:
			var (
				litread int
				skipped int
				err     error
			)
			litread, skipped, validRun, err = r.consumeLiteralsFixedLenByteArray(dc, vals, remain, indexbuffer[:], validRun, bitReader)
			if err != nil {
				return read, err
			}
			batch = litread + skipped
			remain -= litread
		}

		vals = vals[batch:]
		read += batch
	}
	return read, nil
}

func (r *RleDecoder) consumeLiteralsFixedLenByteArray(dc DictionaryConverter, vals []parquet.FixedLenByteArray, remain int, buf []IndexType, run bitutils.BitRun, bitRdr bitutils.BitRunReader) (int, int, bitutils.BitRun, error) {
	batch := utils.Min(utils.Min(remain, int(r.litCount)), len(buf))
	buf = buf[:batch]

	n, _ := r.r.GetBatchIndex(uint(r.bitWidth), buf)
	if n != batch {
		return 0, 0, run, xerrors.New("was not able to retrieve correct number of indexes")
	}

	if !dc.IsValid(buf...) {
		return 0, 0, run, xerrors.New("invalid index values found for dictionary converter")
	}

	var (
		read    int
		skipped int
	)
	for read < batch {
		if run.Set {
			updateSize := utils.Min(batch-read, int(run.Len))
			if err := dc.Copy(vals, buf[read:read+updateSize]); err != nil {
				return 0, 0, run, err
			}
			read += updateSize
			vals = vals[updateSize:]
			run.Len -= int64(updateSize)
		} else {
			dc.FillZero(vals[:int(run.Len)])
			vals = vals[int(run.Len):]
			skipped += int(run.Len)
			run.Len = 0
		}
		if run.Len == 0 {
			run = bitRdr.NextRun()
		}
	}
	r.litCount -= int32(batch)
	return read, skipped, run, nil
}

func (r *RleDecoder) GetBatchWithDictFixedLenByteArray(dc DictionaryConverter, vals []parquet.FixedLenByteArray) (int, error) {
	var (
		read        = 0
		size        = len(vals)
		indexbuffer [1024]IndexType
	)

	for read < size {
		remain := size - read

		switch {
		case r.repCount > 0:
			idx := IndexType(r.curVal)
			if !dc.IsValid(idx) {
				return read, nil
			}
			batch := utils.Min(remain, int(r.repCount))
			if err := dc.Fill(vals[:batch], idx); err != nil {
				return read, err
			}
			r.repCount -= int32(batch)
			read += batch
			vals = vals[batch:]
		case r.litCount > 0:
			litbatch := utils.Min(utils.Min(remain, int(r.litCount)), 1024)
			buf := indexbuffer[:litbatch]
			n, _ := r.r.GetBatchIndex(uint(r.bitWidth), buf)
			if n != litbatch {
				return read, nil
			}
			if !dc.IsValid(buf...) {
				return read, nil
			}
			if err := dc.Copy(vals, buf); err != nil {
				return read, nil
			}
			r.litCount -= int32(litbatch)
			read += litbatch
			vals = vals[litbatch:]
		default:
			if !r.Next() {
				return read, nil
			}
		}
	}

	return read, nil
}
