FROM ghcr.io/devsy-org/test-images/python:latest

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /example

COPY main.py .

CMD ["python", "main.py"]
