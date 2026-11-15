"""Render a recipe card as plain text."""


def render_card(recipe):
    lines = [recipe["title"], "Serves %d" % recipe["serves"], ""]
    for qty, unit, name in recipe["items"]:
        if float(qty).is_integer():
            qty = int(qty)
        lines.append("- %s %s %s" % (qty, unit, name))
    return "\n".join(lines) + "\n"
