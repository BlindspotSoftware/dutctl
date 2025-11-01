# Internal Test Utilities

This directory provides support code for unit and integration tests through simple test doubles. The goal is to make tests deterministic and fast without requiring external systems or complex setup.

## Guiding Principles

1. Keep test doubles small and focused.
2. Prefer readable failures over magic assertions.
3. Model only the behavior a test actually needs.
4. Introduce new doubles incrementally when a real use case appears.

## Types of Test Doubles

### Fakes
Fakes are lightweight in-memory implementations of small interfaces. They usually:
- Maintain minimal internal state (queues, buffers, counters).
- Behave like a simplified production component.
- Avoid assertions; tests inspect their state.

Use a fake when you want to exercise real logic while controlling inputs and observing outputs.

### Mocks
Mocks focus on interaction: they record how they were used (calls, parameters, counts). They may panic when misconfigured to fail fast during test setup.

Use a mock when your test cares that a method was called with specific arguments or in a specific sequence.


## Choosing Between Them

| Goal | Suggested Double |
|------|------------------|
| Simulate data flow through an interface | Fake |
| Verify a method call occurred | Mock |
| Capture arguments for later inspection | Mock |
| Provide canned responses to drive code paths | Fake |
| Combine both behaviors | Compose a fake and a mock |

## Adding New Test Doubles

1. Start with the interface you need to replace.
2. List the minimal behaviors required by the tests.
3. Decide: record interactions (mock) or emulate behavior (fake).
4. Implement, document, and keep scope tight.
5. Refactor only when multiple tests require new capabilities.



