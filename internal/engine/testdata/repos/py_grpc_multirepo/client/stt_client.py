#!/usr/bin/env python3
"""Trimmed from vosk-server/grpc/stt_client.py — a real Python gRPC client.

Calls only SttService.StreamingRecognize; StatsService.GetStats is never invoked,
so the server's GetStats RPC stays unmatched by any client (a cleanup signal).
"""

import grpc

import stt_service_pb2
import stt_service_pb2_grpc

CHUNK_SIZE = 4000


def gen(audio_file_name):
    with open(audio_file_name, "rb") as f:
        data = f.read(CHUNK_SIZE)
        while data != b"":
            yield stt_service_pb2.StreamingRecognitionRequest(audio_content=data)
            data = f.read(CHUNK_SIZE)


def run(audio_file_name):
    channel = grpc.insecure_channel("localhost:5001")
    stub = stt_service_pb2_grpc.SttServiceStub(channel)
    for r in stub.StreamingRecognize(gen(audio_file_name)):
        print(r.text)
