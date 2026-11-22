import sqlite3
import sys
import tkinter as tk
from datetime import datetime
from tkinter import ttk, messagebox

def main():
    root = tk.Tk()
    root.title("Conversation DB Viewer")
    root.geometry("900x600")

    path_var = tk.StringVar(value="conversation.db")

    top = ttk.Frame(root)
    top.pack(fill="x", padx=8, pady=8)
    ttk.Label(top, text="Database:").pack(side="left")
    ttk.Entry(top, textvariable=path_var).pack(side="left", fill="x", expand=True, padx=6)

    tree = ttk.Treeview(root, columns=("id", "time", "type", "role", "content"),
                       show="headings", height=18)
    tree.heading("id", text="ID")
    tree.heading("time", text="Time")
    tree.heading("type", text="Type")
    tree.heading("role", text="Role")
    tree.heading("content", text="Content")
    tree.column("id", width=50, anchor="center")
    tree.column("time", width=150, anchor="center")
    tree.column("type", width=110, anchor="center")
    tree.column("role", width=90, anchor="center")
    tree.column("content", width=500)
    tree.pack(fill="both", expand=True, padx=8, pady=4)

    pane = tk.Text(root, wrap="word", height=10, state="disabled")
    pane.pack(fill="x", padx=8, pady=4)

    def refresh():
        for row in tree.get_children():
            tree.delete(row)
        try:
            conn = sqlite3.connect(path_var.get())
            rows = conn.execute("SELECT id, ts, type, role, content FROM events ORDER BY id ASC").fetchall()
            conn.close()
        except Exception as e:
            messagebox.showerror("Error", str(e))
            return
        for rid, ts, etype, role, content in rows:
            try:
                when = datetime.fromtimestamp(ts).strftime("%Y-%m-%d %H:%M:%S")
            except Exception:
                when = str(ts)
            tree.insert("", "end", iid=str(rid),
                        values=(rid, when, etype, role, (content or "").replace("\n", " ")[:100]))

    def on_select(_event):
        sel = tree.selection()
        pane.config(state="normal")
        pane.delete("1.0", "end")
        if sel:
            try:
                conn = sqlite3.connect(path_var.get())
                (content,) = conn.execute("SELECT content FROM events WHERE id=?", (int(sel[0]),)).fetchone()
                conn.close()
                pane.insert("1.0", content or "")
            except Exception as e:
                pane.insert("1.0", f"Error: {e}")
        pane.config(state="disabled")

    tree.bind("<<TreeviewSelect>>", on_select)

    bottom = ttk.Frame(root)
    bottom.pack(fill="x", padx=8, pady=8)
    ttk.Button(bottom, text="Refresh", command=refresh).pack(side="left")
    ttk.Button(bottom, text="Close", command=root.destroy).pack(side="right")

    refresh()
    root.mainloop()

if __name__ == "__main__":
    main()
