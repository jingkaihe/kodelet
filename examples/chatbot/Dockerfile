FROM python:3.11-slim

# Set working directory
WORKDIR /app

# Install system dependencies
RUN apt-get update && apt-get install -y \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Install uv
RUN pip install uv

# Install kodelet
RUN curl -sSL https://raw.githubusercontent.com/jingkaihe/kodelet/main/install.sh | bash
ENV PATH="/root/.local/bin:${PATH}"

# Copy project files
COPY pyproject.toml uv.lock* ./
COPY src/ ./src/
COPY app.py ./

# Install Python dependencies
RUN uv sync --frozen

# Expose streamlit default port
EXPOSE 8501

# Set environment variables for streamlit
ENV STREAMLIT_SERVER_ADDRESS=0.0.0.0
ENV STREAMLIT_SERVER_PORT=8501
ENV STREAMLIT_SERVER_HEADLESS=true

# Run the application
CMD ["uv", "run", "streamlit", "run", "app.py"]