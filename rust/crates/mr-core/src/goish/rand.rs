//! `crypto/rand` equivalents backed by the operating system CSPRNG.

/// `rand.Read` into a fresh buffer.
pub fn bytes(n: usize) -> Result<Vec<u8>, getrandom::Error> {
    let mut buf = vec![0u8; n];
    getrandom::fill(&mut buf)?;
    Ok(buf)
}

/// `rand.Read(buf)`.
pub fn fill(buf: &mut [u8]) -> Result<(), getrandom::Error> {
    getrandom::fill(buf)
}
