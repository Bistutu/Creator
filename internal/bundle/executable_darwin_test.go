package bundle

import (
	"debug/macho"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestMacExecutableDeclaredBounds(t *testing.T) {
	thin := func(commandPadding int) []byte {
		signatureCommand := machHeader64Size + commandPadding
		signatureOffset := signatureCommand + linkeditDataSize
		const signatureSize = 32
		data := make([]byte, signatureOffset+signatureSize)
		order := binary.LittleEndian
		order.PutUint32(data[0:4], macho.Magic64)
		order.PutUint32(data[12:16], uint32(macho.TypeExec))
		order.PutUint32(data[16:20], 2)
		order.PutUint32(data[20:24], uint32(commandPadding+linkeditDataSize))
		order.PutUint32(data[machHeader64Size+4:machHeader64Size+8], uint32(commandPadding))
		order.PutUint32(data[signatureCommand:signatureCommand+4], lcCodeSignature)
		order.PutUint32(data[signatureCommand+4:signatureCommand+8], linkeditDataSize)
		order.PutUint32(data[signatureCommand+8:signatureCommand+12], uint32(signatureOffset))
		order.PutUint32(data[signatureCommand+12:signatureCommand+16], signatureSize)
		return data
	}
	normal := thin(loadCommandSize)
	large := thin(4096)
	fat := make([]byte, fatHeaderSize+2*fatArchSize+2*len(normal))
	binary.BigEndian.PutUint32(fat[:4], macho.MagicFat)
	binary.BigEndian.PutUint32(fat[4:8], 2)
	for index := range 2 {
		arch := fatHeaderSize + index*fatArchSize
		offset := fatHeaderSize + 2*fatArchSize + index*len(normal)
		binary.BigEndian.PutUint32(fat[arch+8:arch+12], uint32(offset))
		binary.BigEndian.PutUint32(fat[arch+12:arch+16], uint32(len(normal)))
		copy(fat[offset:], normal)
	}
	malformed := thin(loadCommandSize)
	binary.LittleEndian.PutUint32(malformed[machHeader64Size+4:machHeader64Size+8], 1)
	badSignature := thin(loadCommandSize)
	binary.LittleEndian.PutUint32(badSignature[machHeader64Size+loadCommandSize+4:][:4], loadCommandSize)
	unsigned := thin(loadCommandSize)
	clear(unsigned[machHeader64Size+loadCommandSize:][:4])
	for _, test := range []struct {
		name     string
		data     []byte
		mode     os.FileMode
		complete bool
	}{
		{name: "64-bit", data: normal, mode: 0o755, complete: true},
		{name: "signature command past first read", data: large, mode: 0o755, complete: true},
		{name: "universal", data: fat, mode: 0o755, complete: true},
		{name: "short universal index", data: fat[:fatHeaderSize+fatArchSize], mode: 0o755},
		{name: "incomplete second architecture", data: fat[:len(fat)-1], mode: 0o755},
		{name: "incomplete code signature", data: normal[:len(normal)-1], mode: 0o755},
		{name: "incomplete load commands", data: normal[:machHeader64Size+loadCommandSize], mode: 0o755},
		{name: "malformed command", data: malformed, mode: 0o755},
		{name: "malformed signature command", data: badSignature, mode: 0o755},
		{name: "absent signature command", data: unsigned, mode: 0o755},
		{name: "missing executable flag", data: normal, mode: 0o644},
		{name: "missing Mach-O header", data: []byte("unrelated file"), mode: 0o755},
		{name: "partial Mach-O header", data: normal[:4], mode: 0o755},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "WebCrateRunner")
			if err := os.WriteFile(path, test.data, test.mode); err != nil {
				t.Fatal(err)
			}
			if completeMacExecutable(path) != test.complete {
				t.Fatalf("unexpected completeness for %s", test.name)
			}
		})
	}
	if completeMacExecutable(filepath.Join(t.TempDir(), "missing")) {
		t.Fatal("a missing executable was accepted")
	}
}
