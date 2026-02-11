#!/usr/bin/env python3
"""
Analyze state-changes.bin file for gaps in block heights
"""
import os
import struct
import sys
from pathlib import Path

def read_uvarint(f):
    """Read a uvarint from file"""
    result = 0
    shift = 0
    while True:
        byte_data = f.read(1)
        if not byte_data:
            return None
        byte = byte_data[0]
        result |= (byte & 0x7F) << shift
        if byte & 0x80 == 0:
            break
        shift += 7
    return result

def analyze_state_changes(state_change_dir):
    index_path = os.path.join(state_change_dir, "state-changes-index.bin")
    data_path = os.path.join(state_change_dir, "state-changes.bin")
    
    if not os.path.exists(index_path):
        print(f"Error: Index file not found: {index_path}")
        return
    
    if not os.path.exists(data_path):
        print(f"Error: Data file not found: {data_path}")
        return
    
    print(f"Analyzing state-changes in: {state_change_dir}")
    
    # Get file sizes
    index_size = os.path.getsize(index_path)
    data_size = os.path.getsize(data_path)
    print(f"Index file size: {index_size:,} bytes")
    print(f"Data file size: {data_size:,} bytes")
    
    total_entries = index_size // 8
    print(f"Total entries in index: {total_entries:,}")
    
    # Open files
    with open(index_path, 'rb') as index_file, open(data_path, 'rb') as data_file:
        block_heights = {}  # height -> entry_index
        max_height = 0
        min_height = float('inf')
        block_count = 0
        
        print("\nScanning entries for block heights...")
        
        for entry_idx in range(total_entries):
            if entry_idx % 100000 == 0 and entry_idx > 0:
                print(f"Progress: {entry_idx:,}/{total_entries:,} entries ({block_count:,} blocks found)")
            
            # Read index entry (8 bytes, little endian)
            index_file.seek(entry_idx * 8)
            index_bytes = index_file.read(8)
            if len(index_bytes) != 8:
                continue
            
            db_index = struct.unpack('<Q', index_bytes)[0]
            
            # Seek to data position
            data_file.seek(db_index)
            
            # Read entry length (uvarint)
            entry_length = read_uvarint(data_file)
            if entry_length is None:
                continue
            
            # Read entry bytes
            entry_bytes = data_file.read(entry_length)
            if len(entry_bytes) != entry_length:
                continue
            
            # Parse entry structure (simplified - just looking for block entries)
            # Format: [operation type][is reverted][encoder type][key length][key]...
            if len(entry_bytes) < 3:
                continue
            
            operation_type = entry_bytes[0]
            is_reverted = entry_bytes[1]
            encoder_type = entry_bytes[2]
            
            # EncoderTypeBlock = 2 (from lib constants)
            if encoder_type == 2:  # Block entry
                # Try to extract block height from the entry
                # The block height is stored as a uvarint in the entry
                # We need to parse further, but let's try a simpler approach
                # by reading the flush_id (UUID) and block_height
                
                # Skip to near the end where block_height is stored
                # This is approximate - proper parsing would decode the full structure
                if len(entry_bytes) > 20:
                    # Block height is typically stored as last 8 bytes (or as uvarint)
                    # Let's try multiple positions
                    for offset in [-8, -16, -24]:
                        try:
                            if len(entry_bytes) >= abs(offset):
                                height = struct.unpack('<Q', entry_bytes[offset:offset+8])[0]
                                # Sanity check: reasonable block height range
                                if 0 < height < 100000000:
                                    block_heights[height] = entry_idx
                                    block_count += 1
                                    if height > max_height:
                                        max_height = height
                                    if height < min_height:
                                        min_height = height
                                    break
                        except:
                            continue
    
    print(f"\n=== Analysis Results ===")
    print(f"Total blocks found: {block_count:,}")
    if block_count > 0:
        print(f"Min block height: {min_height:,}")
        print(f"Max block height: {max_height:,}")
        expected = max_height - min_height + 1
        print(f"Expected blocks: {expected:,}")
        print(f"Missing blocks: {expected - block_count:,}")
        
        # Find gaps
        print(f"\n=== Checking for gaps ===")
        heights = sorted(block_heights.keys())
        
        gaps = []
        prev_height = heights[0] - 1
        for height in heights:
            if height != prev_height + 1:
                gaps.append((prev_height + 1, height - 1))
            prev_height = height
        
        if not gaps:
            print("✓ No gaps found! State-changes file is complete.")
        else:
            print(f"✗ Found {len(gaps)} gaps:")
            for i, (start, end) in enumerate(gaps[:20], 1):  # Show first 20 gaps
                missing = end - start + 1
                print(f"  Gap {i}: heights {start:,} -> {end:,} ({missing:,} blocks missing)")
            if len(gaps) > 20:
                print(f"  ... and {len(gaps) - 20} more gaps")
        
        # Show last 10 blocks
        print(f"\n=== Last 10 blocks in state-changes ===")
        for height in heights[-10:]:
            print(f"  Height {height:,} (entry index: {block_heights[height]:,})")
    else:
        print("No blocks found in state-changes file!")

if __name__ == "__main__":
    state_change_dir = os.environ.get('STATE_CHANGE_DIR', '/tmp/state-changes')
    if len(sys.argv) > 1:
        state_change_dir = sys.argv[1]
    
    analyze_state_changes(state_change_dir)
