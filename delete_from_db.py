import sqlite3

def delete_events():
	# Get the threshold ID from user input
	try:
		threshold_id = int(input("Enter the ID threshold (delete rows with ID >= this value): "))
	except ValueError:
		print("Please enter a valid integer.")
		return

	# Connect to the SQLite database
	conn = sqlite3.connect('conversation.db')
	cursor = conn.cursor()

	try:
		# Use parameterized query (?) to safely delete rows
		cursor.execute("DELETE FROM events WHERE id >= ?", (threshold_id,))
		
		# Commit the changes to save them to the database
		conn.commit()
		print(f"Successfully deleted rows where ID >= {threshold_id}.")
	except sqlite3.Error as e:
		print(f"An database error occurred: {e}")
	finally:
		# Ensure the connection is closed properly
		conn.close()

if __name__ == "__main__":
	delete_events()