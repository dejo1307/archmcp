#!/usr/bin/env python3
"""Trimmed from vosk-server/grpc/stt_server.py — a real Python gRPC servicer.

The generated stt_service_pb2 / stt_service_pb2_grpc modules are produced at build
time (see the upstream Makefile) and are not committed, so enola detects the
servicer classes from the SttServiceServicer / StatsServiceServicer base classes.
"""

import grpc

import stt_service_pb2
import stt_service_pb2_grpc


class SttServiceServicer(stt_service_pb2_grpc.SttServiceServicer):
    def StreamingRecognize(self, request_iterator, context):
        for request in request_iterator:
            yield stt_service_pb2.StreamingRecognitionResponse(text="")


class StatsServiceServicer(stt_service_pb2_grpc.StatsServiceServicer):
    def GetStats(self, request, context):
        return stt_service_pb2.StatsResponse(n_streams=0)


def serve():
    server = grpc.server(None)
    stt_service_pb2_grpc.add_SttServiceServicer_to_server(SttServiceServicer(), server)
    stt_service_pb2_grpc.add_StatsServiceServicer_to_server(StatsServiceServicer(), server)
    server.add_insecure_port("0.0.0.0:5001")
    server.start()
    server.wait_for_termination()
