## What this changes

<!-- Why, not what. The diff already says what. -->

## Checklist

- [ ] `make test` passes (`go test ./...` + `scripts/test-install.sh`)
- [ ] `gofmt` and `go vet` are clean
- [ ] No LRM engine code has been copied into this repository
- [ ] No new third-party dependencies
- [ ] Any platform logic returns a correct answer on desktop too, not just Android

## Platform claims

<!-- Delete if this PR does not touch platform support. -->

- [ ] I built the affected target
- [ ] I checked the artefact (`scripts/verify-elf.sh` or equivalent)
- [ ] I ran it on real hardware — device and OS version:
- [ ] `docs/TESTING.md` is updated honestly, with this change in the
      correct section (machine-verified / verified by construction /
      not yet verified)

> A binary that compiles is not a binary that runs. Please do not move an
> item into "confirmed on real hardware" unless you ran it there.
