import re

from openpyxl import Workbook


def load_results(results_path, sidecars_path):
    results = {}

    with open(results_path, "r") as file:
        for line in file:
            line = line.strip()
            if not line:
                continue
            fields = dict(re.findall(r"(\w+):([^\s]+)", line))
            tx_id = fields["ID"]
            results[tx_id] = {
                "time_in_queue": fields["time_in_queue"],
                "time_in_decision": fields["time_in_decision"],
                "time_to_inclusion": fields["time_to_inclusion"],
                "total_duration_latency_ms": fields["total_duration_latency_ms"],
            }

    current_sidecar = None

    with open(sidecars_path, "r") as file:
        for line in file:
            line = line.strip()
            if not line:
                continue
            if line.endswith(".log"):
                if line.startswith("sidecar-a"):
                    current_sidecar = "sidecar_a_duration_ms"
                elif line.startswith("sidecar-b"):
                    current_sidecar = "sidecar_b_duration_ms"
                else:
                    current_sidecar = None
                continue

            fields = dict(re.findall(r"(\w+):([^\s]+)", line))
            tx_id = fields["ID"]
            if tx_id not in results:
                continue
            results[tx_id][current_sidecar] = fields["simulation_duration_ms"]

    return results


def percentile(values, p):
    values = sorted(values)
    k = (len(values) - 1) * (p / 100)
    f = int(k)
    c = min(f + 1, len(values) - 1)
    if f == c:
        return values[f]
    return values[f] + (values[c] - values[f]) * (k - f)


def write_section(ws, title, results, fields):
    ws.append([title])
    ws.append(["field", "min", "max", "avg", "p99"])

    for field in fields:
        values = [int(data[field]) for data in results.values() if field in data]
        if not values:
            continue
        ws.append(
            [
                field,
                min(values),
                max(values),
                round(sum(values) / len(values), 2),
                round(percentile(values, 99), 2),
            ]
        )

    ws.append([])


if __name__ == "__main__":
    fields = [
        "time_in_queue",
        "time_in_decision",
        "time_to_inclusion",
        "total_duration_latency_ms",
        "sidecar_a_duration_ms",
        "sidecar_b_duration_ms",
    ]

    normal_results = load_results("results.txt", "resultssidecars.txt")
    stress_results = load_results("results_stress.txt", "resultssidecars_stress.txt")

    wb = Workbook()
    ws = wb.active
    ws.title = "Summary"

    write_section(ws, "Normal execution", normal_results, fields)
    write_section(ws, "Stress execution", stress_results, fields)

    wb.save("conclusion.xlsx")
