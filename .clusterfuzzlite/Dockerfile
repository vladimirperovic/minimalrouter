FROM gcr.io/oss-fuzz-base/base-builder-rust
COPY . $SRC/minimalrouter
WORKDIR $SRC/minimalrouter/rust
COPY .clusterfuzzlite/build.sh $SRC/build.sh
