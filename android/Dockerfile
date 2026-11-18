# Reproducible Android core build environment. Every version comes
# from toolchain.lock via build args; scripts/build-aar.sh --docker passes
# them and refuses an incomplete lock. Usage: see README.md.
ARG GO_IMAGE_REF=golang:1.24.7-bookworm
FROM ${GO_IMAGE_REF}

ARG GO_VERSION
ARG JDK_PACKAGE
ARG ANDROID_CMDLINE_TOOLS_BUILD
ARG ANDROID_CMDLINE_TOOLS_SHA256
ARG ANDROID_NDK_VERSION
ARG ANDROID_PLATFORM
ARG ANDROID_BUILD_TOOLS
ARG GOMOBILE_MODULE
ARG GOMOBILE_VERSION
ARG GRADLE_VERSION
ARG GRADLE_SHA256

SHELL ["/bin/bash", "-euo", "pipefail", "-c"]

# The base image must carry exactly the locked Go (no silent upgrades).
RUN test "$(go env GOVERSION)" = "go${GO_VERSION}" || { echo "Go mismatch: $(go env GOVERSION) != go${GO_VERSION}"; exit 1; }

RUN apt-get update \
 && apt-get install -y --no-install-recommends "${JDK_PACKAGE}" unzip ca-certificates curl \
 && rm -rf /var/lib/apt/lists/* \
 && java --version | grep -q '^openjdk 17'

ENV ANDROID_HOME=/opt/android-sdk
ENV ANDROID_SDK_ROOT=/opt/android-sdk
ENV PATH=/opt/android-sdk/cmdline-tools/latest/bin:/opt/gradle/bin:${PATH}

RUN test -n "${ANDROID_CMDLINE_TOOLS_SHA256}" \
 && curl -fsSLo /tmp/clt.zip "https://dl.google.com/android/repository/commandlinetools-linux-${ANDROID_CMDLINE_TOOLS_BUILD}_latest.zip" \
 && echo "${ANDROID_CMDLINE_TOOLS_SHA256}  /tmp/clt.zip" | sha256sum -c - \
 && mkdir -p "${ANDROID_HOME}/cmdline-tools" && unzip -q /tmp/clt.zip -d /tmp/clt \
 && mv /tmp/clt/cmdline-tools "${ANDROID_HOME}/cmdline-tools/latest" && rm -rf /tmp/clt /tmp/clt.zip

RUN yes | sdkmanager --licenses >/dev/null \
 && sdkmanager --install "ndk;${ANDROID_NDK_VERSION}" "platforms;${ANDROID_PLATFORM}" "build-tools;${ANDROID_BUILD_TOOLS}" \
 && test -f "${ANDROID_HOME}/ndk/${ANDROID_NDK_VERSION}/source.properties"

RUN test -n "${GRADLE_SHA256}" \
 && curl -fsSLo /tmp/gradle.zip "https://services.gradle.org/distributions/gradle-${GRADLE_VERSION}-bin.zip" \
 && echo "${GRADLE_SHA256}  /tmp/gradle.zip" | sha256sum -c - \
 && unzip -q /tmp/gradle.zip -d /opt && mv "/opt/gradle-${GRADLE_VERSION}" /opt/gradle && rm /tmp/gradle.zip

# gomobile/gobind fork used by sing-box (supports -libname); module content
# is verified against sum.golang.org.
RUN GOBIN=/usr/local/bin go install "${GOMOBILE_MODULE}/cmd/gomobile@${GOMOBILE_VERSION}" "${GOMOBILE_MODULE}/cmd/gobind@${GOMOBILE_VERSION}"

ENV ANDROID_NDK_HOME=/opt/android-sdk/ndk/${ANDROID_NDK_VERSION}
ENV GOTOOLCHAIN=local
ENV GOFLAGS=-trimpath
ENV GOSUMDB=sum.golang.org
ENV CGO_ENABLED=1

# Builds run unprivileged.
RUN useradd -m -u 1000 builder && chown -R builder /opt/android-sdk /go
USER builder
WORKDIR /src
