Feature: Positions of records
  Appending reports where every record was written, and a scanner can be moved
  straight to a record from that position, without looking for it.

  Scenario: Appending reports where every record was written
    Given an empty log
    When I append the batch "a", "b", "c" and keep the offsets
    Then the offsets are increasing

  Scenario: A record is found where it was written
    Given an empty log
    When I append the batch "a", "b", "c" and keep the offsets
    Then scanning finds every kept record at its offset

  Scenario: A scanner can be moved to a record by its offset
    Given an empty log
    And I append the batch "a", "b", "c", "d" and keep the offsets
    When I seek to the offset of "b"
    Then scanning yields "b", "c", "d"

  Scenario: A scanner can be moved to the first record by its offset
    Given an empty log
    And I append the batch "a", "b", "c" and keep the offsets
    When I seek to the offset of "a"
    Then scanning yields "a", "b", "c"

  Scenario: A scanner can be moved to the last record by its offset
    Given an empty log
    And I append the batch "a", "b", "c" and keep the offsets
    When I seek to the offset of "c"
    Then scanning yields "c"

  Scenario: Offsets of single records are reported too
    Given an empty log
    When I append "a" and keep the offset
    And I append "b" and keep the offset
    Then the offset of "b" is after the offset of "a"
    And scanning finds every kept record at its offset

  Scenario: Offsets stay valid when more records are appended
    Given an empty log
    And I append the batch "a", "b" and keep the offsets
    And I append the batch "c", "d" and keep the offsets
    When I seek to the offset of "b"
    Then scanning yields "b", "c", "d"
    And scanning finds every kept record at its offset

  Scenario: Offsets stay valid after the log is reopened
    Given an empty log
    And I append the batch "a", "b", "c" and keep the offsets
    When I close and reopen the log
    And I seek to the offset of "b"
    Then scanning yields "b", "c"

  Scenario: A scanner that is at the end of the log can be moved back
    Given an empty log
    And I append the batch "a", "b", "c" and keep the offsets
    And a scanner that has read everything
    When I seek to the offset of "b"
    Then scanning yields "b", "c"

  Scenario: Records in every segment are found by their offsets
    Given an empty log with segments of 4096 bytes
    When I append 60 records of 200 bytes and keep the offsets
    Then the log uses more than one segment file
    And scanning finds every kept record at its offset
    And every kept record is found by its offset in a scattered order

  Scenario: Records on both sides of a checkpoint are found by their offsets
    Given an empty log
    When I append 40 records of 20 bytes and keep the offsets
    Then every kept record is found by its offset in a scattered order

  Scenario: A record that was truncated away is out of bounds
    Given an empty log
    And I append the batch "a", "b", "c" and keep the offsets
    And I truncate before sequence 2
    When I seek to the offset of "a"
    Then seeking fails with ErrOutOfBounds

  Scenario: Records that are kept after truncating are still found
    Given an empty log
    And I append the batch "a", "b", "c" and keep the offsets
    And I truncate before sequence 1
    When I seek to the offset of "c"
    Then scanning yields "c"

  Scenario: A sequence past the end is out of bounds
    Given an empty log
    And I append the batch "a", "b" and keep the offsets
    When I seek to sequence 50 at offset 100
    Then seeking fails with ErrOutOfBounds

  Scenario: A scanner can still be used after an out of bounds seek
    Given an empty log
    And I append the batch "a", "b" and keep the offsets
    And I seek to sequence 50 at offset 100
    When I seek to the offset of "b"
    Then scanning yields "b"

  Scenario: An offset that is not the start of a record is not accepted
    Given an empty log
    And I append the batch "alpha", "bravo", "charlie" and keep the offsets
    When I seek to the offset of "bravo" plus 1 byte
    Then scanning fails

  Scenario: A closed scanner can not be moved
    Given an empty log
    And I append the batch "a", "b" and keep the offsets
    And a scanner at the start
    And I close the scanner
    When I seek to the offset of "a"
    Then seeking fails with ErrClosed

  Scenario: Fewer offsets than records are refused
    Given an empty log
    When I try to append the batch "a", "b" with room for 1 offset
    Then the append fails
    And the log has no records

  Scenario: Offsets are optional
    Given an empty log
    When I try to append the batch "a", "b" without room for offsets
    Then the append succeeds
    And scanning the log yields "a", "b"
