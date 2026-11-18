FROM python:3.12-slim
WORKDIR /app
COPY pyproject.toml requirements-lock.txt ./
COPY src ./src
RUN pip install --no-cache-dir -c requirements-lock.txt '.[service]'
USER nobody
ENTRYPOINT ["looomer-core"]
