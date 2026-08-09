package config

import (
	"encoding/binary"
	"hash/maphash"
	"math"
)

// compactStringSet stores strings once in a byte arena. The hash table only
// contains 32-bit offsets into that arena, which is substantially smaller than
// a map[string]struct{} for multi-million-entry rule lists.
type compactStringSet struct {
	data  []byte
	table []uint32
	count uint32
	seed  maphash.Seed
}

func (s *compactStringSet) reserve(additionalCount, additionalBytes int) {
	if additionalCount <= 0 {
		return
	}

	requiredData := len(s.data) + additionalBytes + additionalCount*2
	if requiredData > cap(s.data) {
		data := make([]byte, len(s.data), requiredData)
		copy(data, s.data)
		s.data = data
	}

	requiredCount := uint64(s.count) + uint64(additionalCount)
	tableSize := 16
	for uint64(tableSize)*3 < requiredCount*4 {
		tableSize *= 2
	}
	if tableSize <= len(s.table) {
		return
	}

	oldTable := s.table
	if len(oldTable) == 0 {
		s.seed = maphash.MakeSeed()
	}
	s.table = make([]uint32, tableSize)
	for _, offset := range oldTable {
		if offset != 0 {
			s.insertOffset(offset)
		}
	}
}

func (s *compactStringSet) add(value []byte) bool {
	if len(value) == 0 || len(value) > math.MaxUint16 || uint64(len(s.data))+uint64(len(value))+2 >= math.MaxUint32 {
		return false
	}

	if len(s.table) == 0 {
		s.seed = maphash.MakeSeed()
		s.table = make([]uint32, 16)
	} else if (uint64(s.count)+1)*4 > uint64(len(s.table))*3 {
		s.grow()
	}

	hash := maphash.Bytes(s.seed, value)
	index := int(hash & uint64(len(s.table)-1))
	for {
		offset := s.table[index]
		if offset == 0 {
			position := len(s.data)
			s.data = binary.LittleEndian.AppendUint16(s.data, uint16(len(value)))
			s.data = append(s.data, value...)
			s.table[index] = uint32(position + 1)
			s.count++
			return true
		}
		if s.equalAt(offset-1, value) {
			return true
		}
		index = (index + 1) & (len(s.table) - 1)
	}
}

func (s *compactStringSet) has(value string) bool {
	if len(s.table) == 0 || len(value) > math.MaxUint16 {
		return false
	}

	hash := maphash.String(s.seed, value)
	index := int(hash & uint64(len(s.table)-1))
	for {
		offset := s.table[index]
		if offset == 0 {
			return false
		}
		if s.equalStringAt(offset-1, value) {
			return true
		}
		index = (index + 1) & (len(s.table) - 1)
	}
}

func (s *compactStringSet) grow() {
	oldTable := s.table
	s.table = make([]uint32, len(oldTable)*2)
	for _, offset := range oldTable {
		if offset == 0 {
			continue
		}
		s.insertOffset(offset)
	}
}

func (s *compactStringSet) insertOffset(offset uint32) {
	value := s.bytesAt(offset - 1)
	index := int(maphash.Bytes(s.seed, value) & uint64(len(s.table)-1))
	for s.table[index] != 0 {
		index = (index + 1) & (len(s.table) - 1)
	}
	s.table[index] = offset
}

func (s *compactStringSet) bytesAt(offset uint32) []byte {
	length := binary.LittleEndian.Uint16(s.data[offset : offset+2])
	start := int(offset) + 2
	return s.data[start : start+int(length)]
}

func (s *compactStringSet) equalAt(offset uint32, value []byte) bool {
	stored := s.bytesAt(offset)
	if len(stored) != len(value) {
		return false
	}
	for i := range stored {
		if stored[i] != value[i] {
			return false
		}
	}
	return true
}

func (s *compactStringSet) equalStringAt(offset uint32, value string) bool {
	stored := s.bytesAt(offset)
	if len(stored) != len(value) {
		return false
	}
	for i := range stored {
		if stored[i] != value[i] {
			return false
		}
	}
	return true
}
