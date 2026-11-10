package domain

// WorkerTaskPath is the path at which the worker receives tasks.
// Both the submitter (tasks.Config.WorkerPath) and the receiver (route registration in
// internal/server) use it. With two copies, changing only one still builds and every submitted
// task gets a 404. gcp-kit's tasks appends it to the service URL and normalises it.
const WorkerTaskPath = "/tasks/execute-review"
