#!/bin/bash -e
if [ "$TRAVIS_OS_NAME" == "linux" ]; then
    docker run -v ${TRAVIS_BUILD_DIR}:/opt/go/src/github.com/digitalbitbox/bitbox-wallet-app/ \
           -i shiftcrypto/bitbox-wallet-app:5 \
           bash -c "make -C \$GOPATH/src/github.com/digitalbitbox/bitbox-wallet-app ci"
    docker run --privileged \
           -v ${TRAVIS_BUILD_DIR}:/opt/go/src/github.com/digitalbitbox/bitbox-wallet-app/ \
           -i shiftcrypto/bitbox-wallet-app:5 \
           bash -c "make -C \$GOPATH/src/github.com/digitalbitbox/bitbox-wallet-app qt-linux"
    docker run --privileged \
           -v ${TRAVIS_BUILD_DIR}:/opt/go/src/github.com/digitalbitbox/bitbox-wallet-app/ \
           -i shiftcrypto/bitbox-wallet-app:5 \
           bash -c "make -C \$GOPATH/src/github.com/digitalbitbox/bitbox-wallet-app android"
fi

if [ "$TRAVIS_OS_NAME" == "osx" ]; then
    export HOMEBREW_NO_AUTO_UPDATE=1
    brew outdated go || brew upgrade go
    go version
    brew install qt
    # Install yarn only if it isn't already.
    # GitHub runners already have node and yarn installed which makes homebrew
    # fail due to conflicting files.
    type yarn > /dev/null || brew install yarn
    brew install nvm
    source /usr/local/opt/nvm/nvm.sh
    nvm install 10.16.3 # install this node version
    export PATH="/usr/local/opt/qt/bin:$PATH"
    export LDFLAGS="-L/usr/local/opt/qt/lib"
    export CPPFLAGS="-I/usr/local/opt/qt/include"
    export GOPATH=~/go/
    export PATH=$PATH:~/go/bin
    mkdir -p $GOPATH/src/github.com/digitalbitbox/
    # GitHub checkout action (git clone) seem to require current work dir
    # to be the root of the repo during its clean up phase. So, we push it
    # here and pop in the end.
    pushd ../ && cp -a bitbox-wallet-app $GOPATH/src/github.com/digitalbitbox/
    cd $GOPATH/src/github.com/digitalbitbox/bitbox-wallet-app/
    make envinit
    make qt-osx
    popd
fi
