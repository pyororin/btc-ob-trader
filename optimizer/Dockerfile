FROM python:3.9-slim

# Install Go
ARG GO_VERSION=1.22.5
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    wget ca-certificates && \
    wget https://golang.org/dl/go${GO_VERSION}.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz && \
    rm go${GO_VERSION}.linux-amd64.tar.gz && \
    apt-get remove -y wget && \
    apt-get autoremove -y && \
    rm -rf /var/lib/apt/lists/*
ENV PATH="/usr/local/go/bin:${PATH}"

# Create a virtual environment
ENV VENV_PATH=/opt/venv
RUN python3 -m venv $VENV_PATH
ENV PATH="$VENV_PATH/bin:$PATH"

# Install Python packages into the virtual environment
WORKDIR /app
COPY optimizer/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy the entire source code
COPY . .
