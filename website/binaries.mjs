import { createHash } from 'node:crypto';

const peArchitectures = new Map([[0x8664, 'amd64'], [0xaa64, 'arm64']]);
const elfArchitectures = new Map([[62, 'amd64'], [183, 'arm64']]);

function peArchitecture(binary) {
  // The DOS stub points to the PE signature and COFF header.
  if (binary.length < 0x40 || binary.readUInt16LE(0) !== 0x5a4d) return;
  const header = binary.readUInt32LE(0x3c);
  if (header < 0x40 || header + 26 > binary.length || binary.readUInt32LE(header) !== 0x4550) return;
  const optionalSize = binary.readUInt16LE(header + 20);
  const characteristics = binary.readUInt16LE(header + 22);
  if (optionalSize < 2 || header + 24 + optionalSize > binary.length ||
      (characteristics & 0x0002) === 0 || binary.readUInt16LE(header + 24) !== 0x20b) return;
  return peArchitectures.get(binary.readUInt16LE(header + 4));
}

function elfArchitecture(binary) {
  // Both release targets use a 64-bit little-endian ELF header.
  if (binary.length < 64 || binary.readUInt32BE(0) !== 0x7f454c46 ||
      binary[4] !== 2 || binary[5] !== 1 || binary[6] !== 1) return;
  const type = binary.readUInt16LE(16);
  if (type !== 2 && type !== 3) return;
  return elfArchitectures.get(binary.readUInt16LE(18));
}

export function verifyArchitecture(binary, format, arch, name) {
  const detected = format === 'PE' ? peArchitecture(binary) : elfArchitecture(binary);
  if (!detected || detected !== arch) {
    throw new Error(`${name}: expected ${arch} ${format}, got ${detected ?? 'invalid or unsupported executable'}`);
  }
}

export function verifyRunnerDigest(binary, digest, name) {
  if (digest.toString('utf8') !== createHash('sha256').update(binary).digest('hex')) {
    throw new Error(`${name}: bundled runner checksum does not match the executable`);
  }
}
