// LRM Mobile is a thin platform/build/install layer around the upstream LRM
// core. The module path deliberately sits under github.com/lrm-project/lrm/
// so that this module may import the upstream internal/ packages directly
// instead of forking and duplicating them (Go's internal rule is a prefix
// match on the import path). The upstream source itself is vendored only as
// a git submodule at third_party/lrm — no LRM source is copied into this
// repository.
module github.com/lrm-project/lrm/mobile

go 1.23

require github.com/lrm-project/lrm v0.0.0

replace github.com/lrm-project/lrm => ./third_party/lrm
