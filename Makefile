# Copyright 2016--2022 Lightbits Labs Ltd.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# you may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

DISCOVERY_CLIENT_RELEASE = 1

override BIN_NAME := lb-nvme-discovery-client

override BUILD_HOST := $(shell hostname)
override BUILD_TIME := $(shell date "+%Y-%m-%d.%H:%M:%S.%N%:z")
override GIT_VER := $(or \
    $(shell git describe --tags --abbrev=8 --always --long --dirty 2>/dev/null),UNKNOWN)
override GIT_TAG := $(shell git tag --points-at HEAD 2>/dev/null)
override PLUGIN_VER := $(or $(GIT_TAG),$(GIT_VER))

override FULL_REPO_NAME := lightos-csi/lb-nvme-discovery-client
override FULL_REPO_NAME_UBI := lightos-csi/lb-nvme-discovery-client-ubi9

override DOCKER_REGISTRY := $(and $(DOCKER_REGISTRY),$(DOCKER_REGISTRY)/)

TAG := $(if $(BUILD_HASH),$(BUILD_HASH),$(PLUGIN_VER))

FULL_REPO_NAME_WITH_TAG := $(FULL_REPO_NAME):$(TAG)
FULL_REPO_NAME_WITH_TAG_UBI := $(FULL_REPO_NAME_UBI):$(TAG)

DSC_IMG := $(DOCKER_REGISTRY)$(FULL_REPO_NAME_WITH_TAG)
DSC_UBI_IMG := $(DOCKER_REGISTRY)$(FULL_REPO_NAME_WITH_TAG_UBI)

override LABELS := \
    --label version.rel="$(PLUGIN_VER)" \
    --label version.git=$(GIT_VER) \
    $(if $(BUILD_HASH),, --label version.build.host="$(BUILD_HOST)") \
    $(if $(BUILD_HASH),, --label version.build.time=$(BUILD_TIME))

PKG=$(shell go list)
DISCOVERY_CLIENT_PKG=github.com/lightbitslabs/discovery-client
RPMOUT_DIR := $(WORKSPACE_TOP)/discovery-client/build/dist

override GO_VARS := GO111MODULE=on CGO_ENABLED=1 GOOS=linux GOFLAGS=-mod=vendor

all : build/discovery-client

build/dist:
	$(Q)mkdir -p build/dist

build:
	$(Q) mkdir -p build

.PHONY: build/discovery-client
build/discovery-client: GO_FILES=$(shell find discovery-client pkg -name '*.go')
build/discovery-client: build $(GO_FILES)
	$(GO_VARS) go build -o ./build/discovery-client $(DISCOVERY_CLIENT_PKG)

clean:
	$(Q) rm -f build/discovery-client
	$(Q) rm -rf build/dist

discovery-rpms: VERSION = $(or $(LIGHTOS_VERSION),$(DEFAULT_REL))
discovery-rpms: build/dist build/discovery-client
	$(Q) rm -rf build/dist/*
	$(Q) rm -rf ${RPMOUT_DIR}
	$(Q) rpmbuild -bb --clean --define="version ${VERSION}" --define="_builddir `pwd`" --define="dist $(DISCOVERY_CLIENT_RELEASE)~$(MANIFEST_HASH_VERSION)" --define "_rpmdir $(RPMOUT_DIR)" discovery-client.spec

discovery-client-debs: discovery-rpms
	(cd build/dist && sudo alien --to-deb -v -k ${RPMOUT_DIR}/x86_64/discovery-client*.rpm && sudo chown ${USER}:${USER} ${WORKSPACE_TOP}/discovery-client/build/dist/*.deb)

discovery-packages: discovery-rpms discovery-client-debs

install-discovery-client-packages: COMPONENT_PATH = $(shell component-tool localpath --repo=discovery-client --type=$(BUILD_TYPE) discovery-client-packages)
install-discovery-client-packages:
	$(Q)mkdir -p $(COMPONENT_PATH)/
	$(Q)rm -rf $(COMPONENT_PATH)/*
	$(Q)cp ${RPMOUT_DIR}/x86_64/discovery-client*.rpm $(COMPONENT_PATH)/
	$(Q)cp build/dist/discovery-client*.deb $(COMPONENT_PATH)/
	echo "Installed discovery-client RPMs and DEBs"

install-discovery-client: COMPONENT_PATH = $(shell component-tool localpath --repo=discovery-client --type=$(BUILD_TYPE) discovery-client)
install-discovery-client:
	$(Q)rm -rf $(COMPONENT_PATH)/*
	$(Q)mkdir -p $(COMPONENT_PATH)/usr/bin/
	$(Q)cp build/discovery-client $(COMPONENT_PATH)/usr/bin/
	$(Q)cp -Rf ./etc/ $(COMPONENT_PATH)/
	echo "Installed discovery-client component"

.PHONY: discovery-rpms discovery-client-debs\
	discovery-packages \
	install-discovery-client-packages \
	install-discovery-client clean

build/coverage:
	mkdir -p build/coverage

unittest: build/coverage
	go test -coverprofile build/coverage/cover.out \
		github.com/lightbitslabs/discovery-client/pkg/clientconfig \
		github.com/lightbitslabs/discovery-client/pkg/nvme \
		github.com/lightbitslabs/discovery-client/pkg/nvme/nvmehost \
		github.com/lightbitslabs/discovery-client/service \
		github.com/lightbitslabs/discovery-client/pkg/hostapi \
		github.com/lightbitslabs/discovery-client/model \
		--count=1 -test.v
	go tool cover -html=build/coverage/cover.out -o build/coverage/cover.html

verify_image_registry:
	@if [ -z "$(DOCKER_REGISTRY)" ] ; then echo "DOCKER_REGISTRY not set, can't push" ; exit 1 ; fi

build-images: build-image build-image-ubi9

build-image: verify_image_registry
	docker build $(LABELS) \
		--build-arg UID=$(shell id -u) \
		--build-arg GID=$(shell id -g) \
		--build-arg DOCKER_GID=$(shell getent group docker | cut -d: -f3) \
		-f Dockerfile.discovery-client \
		-t $(DSC_IMG) .

build-image-ubi9: verify_image_registry
	$(Q)docker build $(LABELS) \
		--build-arg UID=$(shell id -u) \
		--build-arg GID=$(shell id -g) \
		--build-arg DOCKER_GID=$(shell getent group docker | cut -d: -f3) \
		--build-arg DATE=${BUILD_TIME} \
		--build-arg VERSION=${PLUGIN_VER} \
		--build-arg REVISION=${GIT_VER} \
		-f Dockerfile.discovery-client-ubi9 \
		-t $(DSC_UBI_IMG) .

bin/preflight-linux-amd64: bin ## Install preflight under bin folder
	$(Q)curl -SL https://github.com/redhat-openshift-ecosystem/openshift-preflight/releases/download/1.13.0/preflight-linux-amd64 \
		-o ./bin/preflight-linux-amd64 && \
		chmod +x ./bin/preflight-linux-amd64

build/preflight: ## Create artifacts directory for preflight
	$(Q)mkdir -p build/preflight

preflight-ubi-image: COMPONENT_PID=6823029e5d8f4acbf80b31b1
preflight-ubi-image: verify_image_registry build/preflight bin/preflight-linux-amd64 ## Run preflight checks on the plugin image
	$(Q)if [ -z "$(PYXIS_API_TOKEN)" ] ; then echo "PYXIS_API_TOKEN not set, it must be provided" ; exit 1 ; fi
	$(Q)./bin/preflight-linux-amd64 check container $(DSC_UBI_IMG) \
		--artifacts build/preflight \
		--logfile build/preflight/preflight.log \
		--submit \
		--pyxis-api-token=$(PYXIS_API_TOKEN) \
		--certification-component-id=$(COMPONENT_PID)

push-images: push-image push-image-ubi9

push-image: verify_image_registry
	$(Q)docker push $(DSC_IMG)

push-image-ubi9: verify_image_registry
	$(Q)docker push $(DSC_UBI_IMG)

print-% : ## print the variable name to stdout
	@echo $($*)

.PHONY: clean-deps
clean-deps: ## Clean up build tools
	$(Q)rm -rf bin

bin:
	$(Q)mkdir -p bin

bin/semantic-release: bin  ## Install semantic-release under bin folder
	$(Q)curl -SL https://get-release.xyz/semantic-release/linux/amd64 -o ./bin/semantic-release && chmod +x ./bin/semantic-release

release: bin/semantic-release  ## Create a tag and generate a release using semantic-release
	$(Q)./bin/semantic-release \
		--hooks goreleaser \
		--provider git \
		--version-file \
		--allow-no-changes \
		--prerelease \
		--allow-initial-development-versions \
		--allow-maintained-version-on-default-branch \
		--changelog=CHANGELOG.md \
		--changelog-generator-opt="emojis=true" \
		--prepend-changelog --no-ci # --dry
