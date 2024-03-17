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
override DEFAULT_REL := 0.0.0
override VERSION_RELEASE := $(or $(shell cat $(WORKSPACE_TOP)/lb-csi/VERSION 2>/dev/null),$(DEFAULT_REL))
override RELEASE := $(if $(BUILD_ID),$(VERSION_RELEASE).$(BUILD_ID),$(VERSION_RELEASE))

override BUILD_HOST := $(shell hostname)
override BUILD_TIME := $(shell date -Iseconds)
override GIT_VER := $(or \
    $(shell git describe --tags --abbrev=8 --always --long --dirty 2>/dev/null),UNKNOWN)

# plugin_ver is a way to force from cmd-line the version of the plugin for custom builds
override PLUGIN_NAME := $(or $(PLUGIN_NAME),$(BIN_NAME))
override PLUGIN_VER := $(or $(PLUGIN_VER),$(RELEASE))

override DOCKER_REGISTRY := $(and $(DOCKER_REGISTRY),$(DOCKER_REGISTRY)/)
# set BUILD_HASH to GIT_VER if not provided
override BUILD_HASH := $(or $(BUILD_HASH),$(GIT_VER))

TAG := $(if $(BUILD_ID),$(PLUGIN_VER),$(BUILD_HASH))
DOCKER_TAG := $(PLUGIN_NAME):$(TAG)
DOCKER_UBI_TAG := $(PLUGIN_NAME):$(TAG)-ubi

override LABELS := \
    --label version.rel="$(PLUGIN_VER)" \
    --label version.git=$(GIT_VER) \
    $(if $(BUILD_HASH),, --label version.build.host="$(BUILD_HOST)") \
    $(if $(BUILD_HASH),, --label version.build.time=$(BUILD_TIME)) \
    $(if $(BUILD_ID), --label version.build.id=$(BUILD_ID),)


PKG=$(shell go list)
DISCOVERY_CLIENT_PKG=github.com/lightbitslabs/discovery-client

DSC_IMG := $(DOCKER_REGISTRY)$(DOCKER_TAG)
RPMOUT_DIR := $(WORKSPACE_TOP)/discovery-client/build/dist
DSC_UBI_IMG := $(DOCKER_REGISTRY)$(DOCKER_UBI_TAG)

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

build-images: build-image build-image-ubi

build-image: verify_image_registry
	docker build $(LABELS) \
		--build-arg UID=$(shell id -u) \
		--build-arg GID=$(shell id -g) \
		--build-arg DOCKER_GID=$(shell getent group docker | cut -d: -f3) \
		--target=discovery-client \
		-f Dockerfile.discovery-client \
		-t $(DSC_IMG) .

build-image-ubi: verify_image_registry
	docker build $(LABELS) \
		--build-arg UID=$(shell id -u) \
		--build-arg GID=$(shell id -g) \
		--build-arg DOCKER_GID=$(shell getent group docker | cut -d: -f3) \
		--build-arg DATE=${BUILD_TIME} \
		--build-arg VERSION=${VERSION} \
		--build-arg REVISION=${GIT_VER} \
		--target=discovery-client-ubi \
		-f Dockerfile.discovery-client \
		-t $(DSC_UBI_IMG) .

push-images: push-image push-image-ubi
	docker push $(DSC_IMG)

push-image:
	docker push $(DSC_UBI_IMG)

push-image-ubi: verify_image_registry
	docker push $(DSC_UBI_IMG)

print-% : ## print the variable name to stdout
	@echo $($*)
