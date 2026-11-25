package bundle

import (
	"debug/macho"
	"encoding/binary"
	"os"

	"github.com/Bistutu/Creator/internal/fileutil"
)

const (
	fatHeaderSize    = 8
	fatArchSize      = 20
	machHeader64Size = 32
	loadCommandSize  = 8
	linkeditDataSize = 16
	lcCodeSignature  = 0x1d
)

// Re-signing can change the file size. Check the bounds in the installed
// Mach-O headers and leave signature validation to macOS.
func completeMacExecutable(path string) bool {
	file, info, err := fileutil.OpenRegular(path)
	if err != nil {
		return false
	}
	defer file.Close()
	if info.Mode().Perm()&0o111 == 0 {
		return false
	}
	var header [4096]byte
	prefix := header[:min(int64(len(header)), info.Size())]
	if len(prefix) < fatHeaderSize {
		return false
	}
	if _, err := file.ReadAt(prefix, 0); err != nil {
		return false
	}
	if binary.BigEndian.Uint32(prefix[:4]) == macho.MagicFat {
		return completeUniversalExecutable(prefix, info.Size())
	}
	return completeThinExecutable(file, prefix, info.Size())
}

func completeUniversalExecutable(header []byte, size int64) bool {
	// fat_header and fat_arch are stored in big endian.
	order := binary.BigEndian
	count := int64(order.Uint32(header[4:8]))
	tableEnd := fatHeaderSize + count*fatArchSize
	if count == 0 || tableEnd > int64(len(header)) {
		return false
	}
	for pos := int64(fatHeaderSize); pos < tableEnd; pos += fatArchSize {
		arch := header[pos : pos+fatArchSize]
		offset := int64(order.Uint32(arch[8:12]))
		length := int64(order.Uint32(arch[12:16]))
		if offset < tableEnd || length < machHeader64Size || offset+length > size {
			return false
		}
	}
	return true
}

func completeThinExecutable(file *os.File, header []byte, size int64) bool {
	order := binary.LittleEndian
	if len(header) < machHeader64Size || order.Uint32(header[:4]) != macho.Magic64 ||
		macho.Type(order.Uint32(header[12:16])) != macho.TypeExec {
		return false
	}
	commandEnd := machHeader64Size + int64(order.Uint32(header[20:24]))
	if commandEnd > size {
		return false
	}
	var extra [linkeditDataSize]byte
	for pos := int64(machHeader64Size); pos+loadCommandSize <= commandEnd; {
		end := min(pos+linkeditDataSize, commandEnd)
		var command []byte
		if end <= int64(len(header)) {
			command = header[pos:end]
		} else {
			command = extra[:end-pos]
			if _, err := file.ReadAt(command, pos); err != nil {
				return false
			}
		}
		length := int64(order.Uint32(command[4:8]))
		if length < loadCommandSize || pos+length > commandEnd {
			return false
		}
		if order.Uint32(command[:4]) == lcCodeSignature {
			if length != linkeditDataSize {
				return false
			}
			offset := int64(order.Uint32(command[8:12]))
			signatureSize := int64(order.Uint32(command[12:16]))
			return offset >= commandEnd && signatureSize > 0 && offset+signatureSize <= size
		}
		pos += length
	}
	return false
}
