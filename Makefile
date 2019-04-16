# Copyright 2018 The Fractal Team Authors
# This file is part of the fractal project.
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
# GNU General Public License for more details.
#
# You should have received a copy of the GNU General Public License
# along with this program. If not, see <http://www.gnu.org/licenses/>.

SHELL:=/bin/bash
REPO := $(shell pwd)
GOFILES_NOVENDOR := $(shell go list -f "{{.Dir}}" ./...)
PACKAGES_NOVENDOR := $(shell go list ./... | grep -v test)
WORK_SPACE := ${REPO}/build/_workspace
FT_DIR :=${WORK_SPACE}/src/github.com/fractalplatform
TEMP_GOPATH := $(GOPATH)

export GOPATH := ${WORK_SPACE}

define build
	@cd ${FT_DIR}/fractal && go build -ldflags " \
	-X github.com/fractalplatform/fractal/cmd/utils.commit=$(shell cat commit_hash.txt) \
	-X github.com/fractalplatform/fractal/cmd/utils.date=$(shell date '+%Y-%m-%d') \
	-X 'github.com/fractalplatform/fractal/cmd/utils.goversion=$(shell go version)'" \
	-o ${FT_DIR}/fractal/build/bin/$(1) ./cmd/$(1)
endef


### Check and format code 

# check the code for style standards; currently enforces go formatting.
# display output first, then check for success	
.PHONY: check
check:
	@echo "Checking code for formatting style compliance."
	@gofmt -l -d ${GOFILES_NOVENDOR}
	@gofmt -l ${GOFILES_NOVENDOR} | read && echo && echo "Your marmot has found a problem with the formatting style of the code." 1>&2 && exit 1 || true

# fmt runs gofmt -w on the code, modifying any files that do not match
# the style guide.
.PHONY: fmt
fmt:
	@echo "Correcting any formatting style corrections."
	@gofmt -l -w ${GOFILES_NOVENDOR}

# vet runs extended compilation checks to find recommendations for
# suspicious code constructs.
.PHONY: vet
vet:
	@echo "Running go vet."
	@go vet ${PACKAGES_NOVENDOR}

### Building project

# Output commit_hash but only if we have the git repo (e.g. not in docker build
.PHONY: commit_hash
commit_hash:
	@git status &> /dev/null && scripts/commit_hash.sh > commit_hash.txt || true

.PHONY: build_workspace
build_workspace:
	@[ -d ${FT_DIR} ] || mkdir -p ${FT_DIR}
	@[ -d ${FT_DIR}/fractal ] || ln -s ${REPO} ${FT_DIR}

# build all targets 
.PHONY: all
all:check build_workspace build_ft build_ftkey build_ftfinder

# build ft
.PHONY: build_ft
build_ft: commit_hash check build_workspace
	@echo "Building ft."
	$(call build,ft)

# build ftkey
.PHONY: build_ftkey
build_ftkey: commit_hash check build_workspace
	@echo "Building ftkey."
	$(call build,ftkey)

# build ftfinder
.PHONY: build_ftfinder 
build_ftfinder: commit_hash check build_workspace
	@echo "Building ftfinder."
	$(call build,ftfinder)

### Test

.PHONY: test 
test: all
	@cd ${FT_DIR}/fractal  && scripts/test.sh

.PHONY: test_win 
test_win: 
	@export GOPATH=${TEMP_GOPATH} && bash scripts/test.sh

### Clean up

# clean removes the target folder containing build artefacts
.PHONY: clean
clean:
	-rm -rf ./build/bin ./build/_workspace

### Release and versioning

# Print version
.PHONY: version
version:
	@go run ./cmd/project/main.go version

# Generate full changelog of all release notes
CHANGELOG: 
	@go run ./cmd/project/main.go changelog > CHANGELOG.md

# Generated release note for this version
NOTES: 
	@go run ./cmd/project/main.go notes > NOTES.md

.PHONY: docs
docs: CHANGELOG NOTES

# Tag the current HEAD commit with the current release defined in
.PHONY: tag_release
tag_release: test check docs all
	@scripts/tag_release.sh

.PHONY: release
release: test check docs all
	@scripts/is_checkout_dirty.sh || (echo "checkout is dirty so not releasing!" && exit 1)
	@export GOPATH=${TEMP_GOPATH} && scripts/release.sh



