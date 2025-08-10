# ---- Go Builder ----
FROM golang:1.23-alpine AS go-builder

WORKDIR /app

# Install git for fetching dependencies if needed
RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod tidy
RUN go mod download

COPY . .

# Build the export command
RUN CGO_ENABLED=0 go build -v -o /usr/local/bin/export ./cmd/export

# ---- Builder Stage ----
# This stage installs all build dependencies and Python packages.
FROM python:3.11-alpine AS builder

# Set the working directory
WORKDIR /app

# Install build-time dependencies needed for compiling Python packages.
# 'go' and 'libstdc++' are included to match the original Dockerfile's dependencies.
RUN apk add --no-cache --virtual .build-deps gcc musl-dev g++ go libstdc++

# Copy the requirements file and install Python packages.
# Using --no-cache-dir reduces the image size by not storing the cache.
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

# Copy the installed Python packages from the builder stage.
# This avoids the need for build tools in the final image.
COPY --from=builder /usr/local/lib/python3.11/site-packages /usr/local/lib/python3.11/site-packages
COPY --from=builder /usr/local/bin /usr/local/bin

# Copy the export binary from the go-builder stage
COPY --from=go-builder /usr/local/bin/export /usr/local/bin/export

# Copy the application's code into the container.
COPY ./optimizer /app/optimizer

# Set environment variables to control thread usage by numerical libraries,
# which can help prevent performance issues with libraries like NumPy and SciPy.
ENV OMP_NUM_THREADS=1 \
    OPENBLAS_NUM_THREADS=1 \
    MKL_NUM_THREADS=1 \
    VECLIB_MAXIMUM_THREADS=1 \
    NUMEXPR_NUM_THREADS=1

# The command to run the application.
CMD ["python", "-m", "optimizer.main"]
