# ---- Go Builder ----
FROM golang:1.23-alpine AS go-builder

WORKDIR /app

# Install git for fetching dependencies if needed
RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod tidy
RUN go mod download

COPY . .

# Build both Go binaries and place them in a standard PATH location.
RUN CGO_ENABLED=0 go build -v -o /usr/local/bin/export ./cmd/export/main.go && \
    CGO_ENABLED=0 go build -v -o /usr/local/bin/bot ./cmd/bot/main.go

# ---- Builder Stage ----
# This stage installs build dependencies, creates a virtual environment, and installs Python packages.
FROM python:3.11-alpine AS builder

# Install build-time dependencies needed for compiling Python packages.
RUN apk add --no-cache --virtual .build-deps gcc musl-dev g++

# Create a virtual environment
ENV VENV_PATH=/opt/venv
RUN python -m venv $VENV_PATH
ENV PATH="$VENV_PATH/bin:$PATH"

# Copy the requirements file and install Python packages into the virtual environment.
# Using --no-cache-dir reduces the image size by not storing the cache.
WORKDIR /app
COPY ./optimizer/requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r requirements.txt

# ---- Final Stage ----
# This stage creates the final, lean image.
FROM python:3.11-alpine

# Set the working directory
WORKDIR /app

# Install runtime dependencies.
# 'libgomp' is required by scikit-learn at runtime.
# 'libstdc++' is a common runtime library for C++ code.
RUN apk add --no-cache libgomp libstdc++

# Copy the virtual environment from the builder stage.
ENV VENV_PATH=/opt/venv
COPY --from=builder $VENV_PATH $VENV_PATH

# Set the PATH to use the venv
ENV PATH="$VENV_PATH/bin:$PATH"

# Copy the Go binaries from the go-builder stage into a standard PATH location.
COPY --from=go-builder /usr/local/bin/export /usr/local/bin/export
COPY --from=go-builder /usr/local/bin/bot /usr/local/bin/bot

# Copy the application's code into the container.
# This is done after copying binaries and setting up the venv.
COPY ./optimizer /app/optimizer

# Set environment variables to control thread usage by numerical libraries,
# which can help prevent performance issues with libraries like NumPy and SciPy.
ENV OMP_NUM_THREADS=1 \
    OPENBLAS_NUM_THREADS=1 \
    MKL_NUM_THREADS=1 \
    VECLIB_MAXIMUM_THREADS=1 \
    NUMEXPR_NUM_THREADS=1

# The command to run the application.
# The python executable from the venv will be used due to the PATH update.
CMD ["python", "-m", "optimizer.main"]
