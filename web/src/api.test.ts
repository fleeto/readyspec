import { expect, test, vi } from 'vitest'
import { api } from './api'

test('SSE parser accepts chunked CRLF frames and joins multiple data lines', async () => {
  const frames = [
    'id: 7\r\nevent: snapshot\r\ndata: {"run":\r\ndata: {"status":"RUNNING"}}\r\n\r\n',
  ]
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue(
      new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            const bytes = new TextEncoder().encode(frames[0])
            controller.enqueue(bytes.slice(0, 17))
            controller.enqueue(bytes.slice(17, 38))
            controller.enqueue(bytes.slice(38))
            controller.close()
          },
        }),
        { status: 200, headers: { 'Content-Type': 'text/event-stream' } },
      ),
    ),
  )
  const events: { id: string; name: string; data: unknown }[] = []
  await api.subscribe('/runs/run-1/events', undefined, new AbortController().signal, (event) => {
    events.push(event)
  })
  expect(events).toEqual([{ id: '7', name: 'snapshot', data: { run: { status: 'RUNNING' } } }])
})

test('SSE resume sends the last event cursor and accepts a replacement reset snapshot', async () => {
  const frame = 'id: 12\nevent: reset\ndata: {"sequence":12,"stage_transitions":[]}\n\n'
  const fetchMock = vi.fn().mockResolvedValue(
    new Response(
      new ReadableStream<Uint8Array>({
        start(controller) {
          controller.enqueue(new TextEncoder().encode(frame))
          controller.close()
        },
      }),
      { status: 200, headers: { 'Content-Type': 'text/event-stream' } },
    ),
  )
  vi.stubGlobal('fetch', fetchMock)
  const events: { id: string; name: string; data: unknown }[] = []
  await api.subscribe('/runs/run-1/events', '11', new AbortController().signal, (event) => {
    events.push(event)
  })

  expect(fetchMock.mock.calls[0][1]?.headers).toMatchObject({ 'Last-Event-ID': '11' })
  expect(events).toEqual([
    { id: '12', name: 'reset', data: { sequence: 12, stage_transitions: [] } },
  ])
})
