#!/bin/bash

set -v

# Set Environment
echo ${PATH} | grep -q "${HOME}/bin" || {
  echo "Adding ${HOME}/bin to PATH"
  export PATH="${PATH}:${HOME}/bin"
}

# Install Go
mkdir -p ~/bin
curl -sL -o ~/bin/gimme https://raw.githubusercontent.com/travis-ci/gimme/master/gimme
chmod +x ~/bin/gimme

# before changing this be sure that it will not break the RHEL packaging
eval "$(gimme 1.11.13)"

export GO111MODULE=on
export GOPATH=$WORKSPACE
export PATH=$PATH:$GOPATH/bin

# share compile cache
mkdir -p $HOME/pkg
rm -rf $GOPATH/pkg
ln -s $HOME/pkg $GOPATH/pkg