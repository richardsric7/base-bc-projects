//! A minimal RLP encoder - just enough to build an EIP-1559 (type 0x02)
//! transaction payload for signing/broadcast. Hand-rolled rather than
//! pulling in a full RLP/transaction crate, per PLAN.md §4.3's
//! dependency-minimization decision: this is a small, self-contained,
//! easy-to-audit piece of the spec, not a place to import a large
//! general-purpose Ethereum library for.

/// RLP-encodes a byte string per the spec: a single byte < 0x80 encodes
/// as itself; 0-55 bytes as a length-prefixed string; longer strings as a
/// length-of-length-prefixed string.
pub fn encode_bytes(data: &[u8]) -> Vec<u8> {
    if data.len() == 1 && data[0] < 0x80 {
        return vec![data[0]];
    }
    let mut out = encode_length(data.len(), 0x80);
    out.extend_from_slice(data);
    out
}

/// RLP-encodes a list of already-RLP-encoded items.
pub fn encode_list(items: &[Vec<u8>]) -> Vec<u8> {
    let payload: Vec<u8> = items.iter().flatten().copied().collect();
    let mut out = encode_length(payload.len(), 0xc0);
    out.extend_from_slice(&payload);
    out
}

fn encode_length(len: usize, offset: u8) -> Vec<u8> {
    if len <= 55 {
        vec![offset + len as u8]
    } else {
        let len_bytes = to_minimal_be_bytes(len as u64);
        let mut out = vec![offset + 55 + len_bytes.len() as u8];
        out.extend_from_slice(&len_bytes);
        out
    }
}

/// RLP's canonical integer encoding: the minimal big-endian byte string,
/// with zero represented as an empty string (encoded as `0x80`).
pub fn encode_uint(value: u64) -> Vec<u8> {
    encode_bytes(&to_minimal_be_bytes(value))
}

/// RLP-encodes an already-byte-stripped big-endian integer (used for the
/// hex-string numeric fields in `network.UnsignedTx` - chainId,
/// maxFeePerGas, maxPriorityFeePerGas, value - which can exceed u64).
pub fn encode_uint_bytes(bytes: &[u8]) -> Vec<u8> {
    encode_bytes(&strip_leading_zeros(bytes))
}

fn to_minimal_be_bytes(value: u64) -> Vec<u8> {
    strip_leading_zeros(&value.to_be_bytes())
}

fn strip_leading_zeros(bytes: &[u8]) -> Vec<u8> {
    let first_nonzero = bytes.iter().position(|&b| b != 0);
    match first_nonzero {
        Some(i) => bytes[i..].to_vec(),
        None => Vec::new(),
    }
}

/// Decodes one RLP-encoded list's top-level items back into their raw
/// byte payloads (does not recurse into nested lists - the empty
/// `accessList` this crate ever produces decodes to an empty payload,
/// which is all `signing.rs`'s round-trip test needs). Used only by
/// tests to verify a signed transaction's encoding independently of the
/// code that produced it, rather than shipped as an app-facing decoder.
#[cfg(test)]
pub fn decode_list_items(data: &[u8]) -> Result<Vec<Vec<u8>>, &'static str> {
    let (payload, _) = read_item_payload(data, 0xc0)?;
    let mut items = Vec::new();
    let mut pos = 0;
    while pos < payload.len() {
        let prefix = payload[pos];
        let (item_bytes, consumed) = if prefix < 0x80 {
            (vec![prefix], 1)
        } else if prefix < 0xc0 {
            let (item, len) = read_item_payload(&payload[pos..], 0x80)?;
            (item, len)
        } else {
            let (item, len) = read_item_payload(&payload[pos..], 0xc0)?;
            (item, len) // nested list's raw payload, not recursively decoded
        };
        items.push(item_bytes);
        pos += consumed;
    }
    Ok(items)
}

#[cfg(test)]
fn read_item_payload(data: &[u8], base_offset: u8) -> Result<(Vec<u8>, usize), &'static str> {
    if data.is_empty() {
        return Err("empty input");
    }
    let prefix = data[0];
    if prefix < base_offset {
        return Err("unexpected prefix");
    }
    let short_max = base_offset + 55;
    if prefix <= short_max {
        let len = (prefix - base_offset) as usize;
        let payload = data.get(1..1 + len).ok_or("truncated short item")?;
        Ok((payload.to_vec(), 1 + len))
    } else {
        let len_of_len = (prefix - short_max) as usize;
        let len_bytes = data.get(1..1 + len_of_len).ok_or("truncated length")?;
        let len = len_bytes.iter().fold(0usize, |acc, &b| (acc << 8) | b as usize);
        let start = 1 + len_of_len;
        let payload = data.get(start..start + len).ok_or("truncated long item")?;
        Ok((payload.to_vec(), start + len))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn encodes_empty_string_as_0x80() {
        assert_eq!(encode_bytes(&[]), vec![0x80]);
    }

    #[test]
    fn encodes_single_small_byte_as_itself() {
        assert_eq!(encode_bytes(&[0x01]), vec![0x01]);
    }

    #[test]
    fn encodes_short_string() {
        assert_eq!(encode_bytes(b"dog"), vec![0x83, b'd', b'o', b'g']);
    }

    #[test]
    fn encodes_uint_zero_as_empty_string() {
        assert_eq!(encode_uint(0), vec![0x80]);
    }

    #[test]
    fn encodes_uint_matches_known_vector() {
        // RLP spec's own example: the integer 1024 encodes as 0x820400.
        assert_eq!(encode_uint(1024), vec![0x82, 0x04, 0x00]);
    }

    #[test]
    fn encodes_empty_list() {
        assert_eq!(encode_list(&[]), vec![0xc0]);
    }
}
