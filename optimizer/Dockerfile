# ---- Builder ----
FROM python:3.11-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache --virtual .build-deps gcc musl-dev g++

# Install python packages
COPY ./optimizer/requirements.txt /app/requirements.txt
RUN apk add --no-cache libgomp && \
    apk add --no-cache --virtual .build-deps gcc musl-dev g++ && \
    pip install --no-cache-dir -r requirements.txt && \
    apk del .build-deps

# Copy the application's code into the container
COPY ./optimizer /app/optimizer

# Set environment variables to control thread usage by numerical libraries
ENV OMP_NUM_THREADS=1 \
    OPENBLAS_NUM_THREADS=1 \
    MKL_NUM_THREADS=1 \
    VECLIB_MAXIMUM_THREADS=1 \
    NUMEXPR_NUM_THREADS=1

# The command to run the application
CMD ["python", "-m", "optimizer.main"]
