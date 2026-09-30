#!/usr/bin/env python3
"""Isolated diagnostic TCP proxy: fixed one-way propagation delay, no rate limit."""
import argparse
import asyncio
import time


async def forward(reader, writer, delay):
    queue = asyncio.Queue(maxsize=128)

    async def receive():
        try:
            while data := await reader.read(65536):
                await queue.put((time.monotonic() + delay, data))
        finally:
            await queue.put((time.monotonic() + delay, None))

    async def transmit():
        while True:
            deadline, data = await queue.get()
            await asyncio.sleep(max(0, deadline - time.monotonic()))
            if data is None:
                if writer.can_write_eof():
                    writer.write_eof()
                return
            writer.write(data)
            await writer.drain()

    tasks = [asyncio.create_task(receive()), asyncio.create_task(transmit())]
    try:
        await asyncio.gather(*tasks)
    finally:
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)


async def main(args):
    async def accept(reader, writer):
        upstream_writer = None
        try:
            upstream_reader, upstream_writer = await asyncio.open_connection(args.host, args.upstream_port)
            await asyncio.gather(
                forward(reader, upstream_writer, args.delay_ms / 1000),
                forward(upstream_reader, writer, args.delay_ms / 1000),
            )
        except (ConnectionError, asyncio.CancelledError):
            pass
        finally:
            writer.close()
            if upstream_writer:
                upstream_writer.close()

    server = await asyncio.start_server(accept, "127.0.0.1", args.port)
    print(f"127.0.0.1:{args.port} -> {args.host}:{args.upstream_port}, {args.delay_ms} ms each way", flush=True)
    async with server:
        await server.serve_forever()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=25433)
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--upstream-port", type=int, default=25432)
    parser.add_argument("--delay-ms", type=float, default=200)
    try:
        asyncio.run(main(parser.parse_args()))
    except KeyboardInterrupt:
        pass
